package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"yousage/internal/managedprocess"
	"yousage/internal/provider/claudeprobe"
)

type NativeOptions struct {
	Process              managedprocess.Options
	IsEnabled            bool
	IsManagedAuth        bool
	ExpectedAccountEmail string
	ExpectedWorkspaceId  string
	Version              string
	ProbeLimit           time.Duration
	EnrichmentLimit      time.Duration
}
type nativeFrame struct {
	Id     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}
type incoming struct {
	frame nativeFrame
	err   error
}

// codexRPCError contains only a numeric protocol code and an allowlisted category.
type codexRPCError struct {
	code     int
	category string
}

func (failure *codexRPCError) Error() string {
	return "Codex could not read usage (JSON-RPC " + strconv.Itoa(failure.code) + "; " + failure.category + ")."
}

// codexErrorCategory consumes untrusted text without retaining any part of it.
func codexErrorCategory(code int, message string) string {
	message = strings.ToLower(message)
	switch {
	case strings.Contains(message, "unauthorized"), strings.Contains(message, "authentication"), strings.Contains(message, "unauthenticated"), strings.Contains(message, "sign in"), code == 401:
		return "authentication"
	case strings.Contains(message, "rate limit"), strings.Contains(message, "rate_limit"), strings.Contains(message, "too many requests"), code == 429:
		return "rate-limited"
	case strings.Contains(message, "network"), strings.Contains(message, "connection"), strings.Contains(message, "dns"), strings.Contains(message, "timed out"), strings.Contains(message, "timeout"):
		return "network"
	case strings.Contains(message, "internal server"), strings.Contains(message, "service unavailable"), code == -32603, code >= 500 && code <= 599:
		return "server"
	default:
		return "unknown"
	}
}

func codexProbeFailure(err error, fallback string) string {
	var rpcError *codexRPCError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "Codex usage probe timed out."
	case errors.Is(err, context.Canceled):
		return "Codex usage probe was cancelled."
	case errors.As(err, &rpcError):
		return rpcError.Error()
	case errors.Is(err, io.EOF):
		return "Codex exited before it could report usage."
	default:
		return fallback
	}
}

type codexSession struct {
	managed  managedprocess.Process
	frames   chan incoming
	cancel   context.CancelFunc
	workers  sync.WaitGroup
	snapshot json.RawMessage
}

func openCodex(ctx context.Context, options NativeOptions) (*codexSession, error) {
	process := options.Process
	process.Arguments = append([]string{"app-server"}, process.Arguments...)
	managed, err := managedprocess.Start(process)
	if err != nil {
		return nil, errors.New("Codex could not be started to read usage")
	}
	reader, cancel := context.WithCancel(ctx)
	session := &codexSession{managed: managed, frames: make(chan incoming, 32), cancel: cancel}
	session.workers.Add(2)
	go func() {
		defer session.workers.Done()
		scanner := bufio.NewScanner(managed.Output)
		scanner.Buffer(make([]byte, 65536), 1<<20)
		for scanner.Scan() {
			data := strings.TrimSpace(scanner.Text())
			if data == "" {
				continue
			}
			var frame nativeFrame
			if !strings.HasPrefix(data, "{") || json.Unmarshal([]byte(data), &frame) != nil {
				select {
				case session.frames <- incoming{err: errors.New("invalid Codex response")}:
				case <-reader.Done():
				}
				return
			}
			select {
			case session.frames <- incoming{frame: frame}:
			case <-reader.Done():
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case session.frames <- incoming{err: err}:
		case <-reader.Done():
		}
	}()
	go func() { defer session.workers.Done(); _, _ = io.Copy(io.Discard, managed.Errors) }()
	return session, nil
}
func (session *codexSession) close() {
	session.cancel()
	session.managed.Stop()
	_ = session.managed.Wait()
	_ = session.managed.Input.Close()
	_ = session.managed.Output.Close()
	_ = session.managed.Errors.Close()
	session.workers.Wait()
}
func (session *codexSession) request(ctx context.Context, id int, method string, params any) (json.RawMessage, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if json.NewEncoder(session.managed.Input).Encode(map[string]any{"id": id, "method": method, "params": params}) != nil {
		return nil, errors.New("Codex request could not be sent")
	}
	for {
		select {
		case received := <-session.frames:
			if received.err != nil {
				return nil, received.err
			}
			frame := received.frame
			if frame.Method == "account/rateLimits/updated" {
				var notification struct {
					Snapshot json.RawMessage `json:"rateLimits"`
				}
				if json.Unmarshal(frame.Params, &notification) == nil {
					session.snapshot = MergeCodexSnapshot(session.snapshot, notification.Snapshot)
				}
			}
			if string(frame.Id) != strconv.Itoa(id) {
				continue
			}
			if frame.Error != nil {
				return nil, &codexRPCError{code: frame.Error.Code, category: codexErrorCategory(frame.Error.Code, frame.Error.Message)}
			}
			if frame.Result == nil {
				return nil, errors.New("invalid Codex response")
			}
			return frame.Result, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
func (session *codexSession) initialize(ctx context.Context, version string) error {
	if version == "" {
		version = "development"
	}
	_, err := session.request(ctx, 1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "Yousage", "title": "Yousage", "version": version}, "capabilities": map[string]bool{"experimentalApi": true}})
	if err != nil {
		return err
	}
	return json.NewEncoder(session.managed.Input).Encode(map[string]string{"method": "initialized"})
}

// ProbeCodex reads identity and limits without model turns. A rate-limits RPC
// error receives at most one retry within the original enrichment deadline.
func ProbeCodex(ctx context.Context, options NativeOptions) Account {
	checkedAt := checked(time.Now())
	account := Account{Limits: unavailable(checkedAt, "unsupported", "")}
	if !options.IsEnabled || options.IsManagedAuth {
		return account
	}
	limit := options.ProbeLimit
	if limit <= 0 {
		limit = 10 * time.Second
	}
	deadline, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	failure := func(message string) Account {
		account.Limits = unavailable(checkedAt, "probeFailed", message)
		return account
	}
	session, err := openCodex(deadline, options)
	if err != nil {
		return failure("Codex could not be started to read usage.")
	}
	defer session.close()
	if err := session.initialize(deadline, options.Version); err != nil {
		return failure(codexProbeFailure(err, "Codex did not answer the initialization request."))
	}
	data, err := session.request(deadline, 2, "account/read", map[string]any{})
	if err != nil {
		return failure(codexProbeFailure(err, "Codex could not read account identity."))
	}
	var identity struct {
		Account *struct {
			Type  string `json:"type"`
			Email string `json:"email"`
			Plan  string `json:"planType"`
		} `json:"account"`
	}
	if json.Unmarshal(data, &identity) != nil {
		return failure("Codex returned an invalid account identity.")
	}
	if identity.Account == nil || identity.Account.Type != "chatgpt" {
		return account
	}
	account.Email, account.Plan = identity.Account.Email, identity.Account.Plan
	enrichment := options.EnrichmentLimit
	if enrichment <= 0 {
		enrichment = 3 * time.Second
	}
	quotaContext, quotaCancel := context.WithTimeout(deadline, enrichment)
	defer quotaCancel()
	data, err = session.request(quotaContext, 3, "account/rateLimits/read", nil)
	var rpcError *codexRPCError
	if errors.As(err, &rpcError) && quotaContext.Err() == nil {
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-quotaContext.Done():
			err = quotaContext.Err()
		case <-timer.C:
			data, err = session.request(quotaContext, 4, "account/rateLimits/read", nil)
		}
		timer.Stop()
	}
	if err != nil {
		return failure(codexProbeFailure(err, "Codex did not answer the usage request."))
	}
	limits, err := MapCodex(data, checkedAt)
	if err != nil {
		return failure("Codex returned invalid usage limits.")
	}
	var owner struct {
		AccountId string `json:"accountId"`
	}
	if json.Unmarshal(data, &owner) == nil {
		account.WorkspaceId = strings.TrimSpace(owner.AccountId)
	}
	account.Limits = limits
	return account
}

// ProbeClaude adapts the controlled initialize/get_usage transport and preserves its selected account identity.
func ProbeClaude(ctx context.Context, options claudeprobe.Options, isEnabled bool) Account {
	checkedAt := checked(time.Now())
	account := Account{Limits: unavailable(checkedAt, "unsupported", "")}
	if !isEnabled {
		return account
	}
	result, err := claudeprobe.Probe(ctx, options)
	if err != nil {
		kind := "transport"
		var failure *claudeprobe.Failure
		if errors.As(err, &failure) {
			kind = failure.Kind
		} else if errors.Is(err, context.DeadlineExceeded) {
			kind = "timeout"
		} else if errors.Is(err, context.Canceled) {
			kind = "cancelled"
		}
		account.Limits = unavailable(checkedAt, "probeFailed", claudeProbeFailure(kind))
		return account
	}
	var identity struct {
		Account *struct {
			Email string `json:"email"`
			Plan  string `json:"subscriptionType"`
		} `json:"account"`
	}
	if json.Unmarshal(result.Initialization, &identity) == nil && identity.Account != nil {
		account.Email, account.Plan = identity.Account.Email, identity.Account.Plan
	}
	if result.State == "failed" {
		if result.FailureKind != "unsupported_method" {
			account.Limits = unavailable(checkedAt, "probeFailed", claudeProbeFailure(result.FailureKind))
		}
		return account
	}
	limits, _, err := MapClaude(result.Usage, checkedAt)
	if err != nil {
		account.Limits = unavailable(checkedAt, "probeFailed", "Claude returned invalid usage limits.")
		return account
	}
	account.Limits = limits
	return account
}

// claudeProbeFailure maps known control-protocol evidence without exposing CLI output or paths.
func claudeProbeFailure(kind string) string {
	switch kind {
	case "authentication":
		return "Claude Code rejected authentication for the usage probe."
	case "timeout":
		return "Claude Code usage probe timed out."
	case "cancelled":
		return "Claude Code usage probe was cancelled."
	case "protocol":
		return "Claude Code returned an invalid usage probe response."
	default:
		return "Claude Code usage probe could not communicate with the CLI."
	}
}
