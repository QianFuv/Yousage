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
	Process         managedprocess.Options
	IsEnabled       bool
	IsManagedAuth   bool
	Version         string
	ProbeLimit      time.Duration
	EnrichmentLimit time.Duration
}
type nativeFrame struct {
	Id     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code int `json:"code"`
	} `json:"error"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}
type incoming struct {
	frame nativeFrame
	err   error
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
				return nil, errors.New("Codex could not read usage (JSON-RPC " + strconv.Itoa(frame.Error.Code) + ").")
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

// ProbeCodex reads account identity and main subscription limits without sending a model turn.
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
	if session.initialize(deadline, options.Version) != nil {
		return failure("Codex did not answer the initialization request.")
	}
	data, err := session.request(deadline, 2, "account/read", map[string]any{})
	if err != nil {
		return failure("Codex could not read account identity.")
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
	if err != nil {
		message := "Codex did not answer the usage request."
		if errors.Is(err, io.EOF) {
			message = "Codex exited before it could report usage."
		}
		if strings.HasPrefix(err.Error(), "Codex could not read usage (JSON-RPC ") {
			message = err.Error()
		}
		return failure(message)
	}
	limits, err := MapCodex(data, checkedAt)
	if err != nil {
		return failure("Codex returned invalid usage limits.")
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
		account.Limits = unavailable(checkedAt, "probeFailed", "Claude could not initialize its usage probe.")
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
		account.Limits = unavailable(checkedAt, "probeFailed", "Claude could not read usage limits.")
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

func AntigravityLimits(now time.Time) Limits {
	return unavailable(checked(now), "unsupported", "The pinned Antigravity driver does not report native subscription limits.")
}
