// Package claudeprobe reads account and usage control responses without sending model turns.
package claudeprobe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maximumFrameBytes = 1024 * 1024

// Options identifies an explicitly selected CLI and isolated account configuration.
type Options struct {
	Executable      string
	Arguments       []string
	Directory       string
	ConfigDirectory string
	Environment     []string
	InitializeLimit time.Duration
	UsageLimit      time.Duration
}

// Result preserves initialization even when the separately timed usage read fails.
type Result struct {
	Initialization json.RawMessage `json:"initialization"`
	Usage          json.RawMessage `json:"usage,omitempty"`
	State          string          `json:"state"`
	Failure        string          `json:"failure,omitempty"`
	FailureKind    string          `json:"failureKind,omitempty"`
}

// Failure distinguishes transport, protocol, capability and authentication failures.
type Failure struct {
	Stage string
	Kind  string
	Cause error
}

// Error describes the failed control stage without discarding its wrapped cause.
func (failure *Failure) Error() string {
	return fmt.Sprintf("%s: %v", failure.Stage, failure.Cause)
}

// Unwrap preserves cancellation/deadline matching for callers.
func (failure *Failure) Unwrap() error { return failure.Cause }

type response struct {
	Type     string `json:"type"`
	Response struct {
		Subtype   string          `json:"subtype"`
		RequestId string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
		Error     string          `json:"error"`
	} `json:"response"`
}

type frame struct {
	message response
	failure error
}

type process struct {
	input  io.WriteCloser
	output io.ReadCloser
	errors io.ReadCloser
	stop   func()
	wait   func() error
}

// Probe performs initialize then get_usage, with independent deadlines and full managed cleanup.
func Probe(ctx context.Context, options Options) (Result, error) {
	if !filepath.IsAbs(options.Executable) {
		return Result{}, errors.New("CLI executable must be an absolute path")
	}
	if options.InitializeLimit <= 0 {
		options.InitializeLimit = 25 * time.Second
	}
	if options.UsageLimit <= 0 {
		options.UsageLimit = 4 * time.Second
	}
	initializeContext, cancelInitialize := context.WithTimeout(ctx, options.InitializeLimit)
	defer cancelInitialize()
	if err := initializeContext.Err(); err != nil {
		return Result{}, err
	}
	managed, err := startProcess(options)
	if err != nil {
		return Result{}, fmt.Errorf("start Claude control process: %w", err)
	}
	readContext, cancelRead := context.WithCancel(ctx)
	frames := make(chan frame, 16)
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		readFrames(readContext, managed.output, frames)
	}()
	go func() {
		defer readers.Done()
		io.Copy(io.Discard, managed.errors)
	}()
	defer func() {
		cancelRead()
		managed.input.Close()
		managed.stop()
		managed.wait()
		managed.output.Close()
		managed.errors.Close()
		readers.Wait()
	}()
	encoder := json.NewEncoder(managed.input)
	initialization, err := request(initializeContext, encoder, frames, "initialize-1", map[string]any{"subtype": "initialize"})
	if err != nil {
		return Result{}, &Failure{Stage: "initialize", Kind: failureKind(err), Cause: err}
	}
	if len(initialization) == 0 || initialization[0] != '{' {
		return Result{}, &Failure{Stage: "initialize", Kind: "protocol", Cause: errors.New("invalid initialization body")}
	}
	cancelInitialize()
	result := Result{Initialization: initialization, State: "failed"}
	usageContext, cancelUsage := context.WithTimeout(ctx, options.UsageLimit)
	defer cancelUsage()
	usage, err := request(usageContext, encoder, frames, "usage-2", map[string]any{"subtype": "get_usage", "skip_behaviors": true})
	if err != nil && isUnsupportedSkipField(err) {
		usage, err = request(usageContext, encoder, frames, "usage-3", map[string]any{"subtype": "get_usage"})
	}
	if err != nil {
		result.Failure = err.Error()
		result.FailureKind = failureKind(err)
		return result, nil
	}
	state, err := validateUsage(usage)
	if err != nil {
		result.Failure = err.Error()
		result.FailureKind = "protocol"
		return result, nil
	}
	result.Usage, result.State = usage, state
	return result, nil
}

func failureKind(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "authentication") || strings.Contains(message, "unauthorized") || strings.Contains(message, "login required") {
		return "authentication"
	}
	if strings.Contains(message, "unknown") && strings.Contains(message, "get_usage") {
		return "unsupported_method"
	}
	return "transport"
}

func request(ctx context.Context, encoder *json.Encoder, frames <-chan frame, identity string, body map[string]any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := encoder.Encode(map[string]any{"type": "control_request", "request_id": identity, "request": body}); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case incoming := <-frames:
			if incoming.failure != nil {
				return nil, incoming.failure
			}
			message := incoming.message
			if message.Type != "control_response" || message.Response.RequestId != identity {
				continue
			}
			if message.Response.Subtype == "error" {
				return nil, errors.New(message.Response.Error)
			}
			if message.Response.Subtype != "success" || len(message.Response.Response) == 0 {
				return nil, errors.New("invalid correlated control response")
			}
			return message.Response.Response, nil
		}
	}
}

func readFrames(ctx context.Context, output io.Reader, frames chan<- frame) {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), maximumFrameBytes)
	total := 0
	for scanner.Scan() {
		data := scanner.Bytes()
		total += len(data)
		if total > 16*maximumFrameBytes || !utf8.Valid(data) {
			publishFrame(ctx, frames, frame{failure: errors.New("control output exceeds limits or is not UTF-8")})
			return
		}
		var message response
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		if !publishFrame(ctx, frames, frame{message: message}) {
			return
		}
	}
	failure := scanner.Err()
	if failure == nil {
		failure = io.EOF
	}
	publishFrame(ctx, frames, frame{failure: failure})
}

func publishFrame(ctx context.Context, frames chan<- frame, incoming frame) bool {
	select {
	case frames <- incoming:
		return true
	case <-ctx.Done():
		return false
	}
}

func isUnsupportedSkipField(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "skip_behaviors") && (strings.Contains(message, "unknown") || strings.Contains(message, "unrecognized") || strings.Contains(message, "unexpected"))
}

func validateUsage(data json.RawMessage) (string, error) {
	var usage struct {
		Available *bool                      `json:"rate_limits_available"`
		Limits    map[string]json.RawMessage `json:"rate_limits"`
	}
	if err := json.Unmarshal(data, &usage); err != nil || usage.Available == nil {
		return "", errors.New("invalid usage capability response")
	}
	if !*usage.Available {
		return "unsupported", nil
	}
	for _, data := range usage.Limits {
		if string(data) == "null" {
			continue
		}
		var window struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    string   `json:"resets_at"`
		}
		if json.Unmarshal(data, &window) != nil || window.Utilization == nil || *window.Utilization < 0 || *window.Utilization > 100 {
			return "", errors.New("invalid usage window")
		}
		if window.ResetsAt != "" {
			if _, err := time.Parse(time.RFC3339Nano, window.ResetsAt); err != nil {
				return "", errors.New("invalid usage reset time")
			}
		}
	}
	return "available", nil
}

func launchArguments(options Options) []string {
	return append(append([]string{}, options.Arguments...), "--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--setting-sources=user,project,local", "--strict-mcp-config", "--no-session-persistence", "--settings", `{"disableAllHooks":true}`)
}

func launchEnvironment(options Options) []string {
	values := map[string]string{}
	for _, entry := range append(os.Environ(), options.Environment...) {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			if runtime.GOOS == "windows" {
				key = strings.ToUpper(key)
			}
			values[key] = value
		}
	}
	delete(values, "FORCE_CODE_TERMINAL")
	delete(values, "NODE_OPTIONS")
	if values["DEBUG_CLAUDE_AGENT_SDK"] == "" {
		delete(values, "DEBUG")
	}
	values["ENABLE_CLAUDEAI_MCP_SERVERS"] = "false"
	values["CLAUDE_CODE_AUTO_CONNECT_IDE"] = "0"
	values["CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL"] = "1"
	if values["CLAUDE_CODE_ENTRYPOINT"] == "" {
		values["CLAUDE_CODE_ENTRYPOINT"] = "sdk-ts"
	}
	if options.ConfigDirectory != "" {
		values["CLAUDE_CONFIG_DIR"] = options.ConfigDirectory
	}
	environment := make([]string, 0, len(values))
	for key, value := range values {
		environment = append(environment, key+"="+value)
	}
	return environment
}
