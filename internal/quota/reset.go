package quota

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

type ResetResult struct {
	Outcome     string `json:"outcome"`
	Warning     string `json:"warning,omitempty"`
	OperationId string `json:"operationId,omitempty"`
}

// Consume redeems a stable account/credit identity, then clears the hub cooldown.
func (hub HubClient) Consume(ctx context.Context, config HubConfig, accountId, creditId string) ResetResult {
	if !config.IsEnabled || accountId == "" || creditId == "" {
		return ResetResult{Outcome: "failed", Warning: "The reset target is unavailable."}
	}
	accounts, err := hub.authFiles(ctx, config)
	if err != nil {
		return ResetResult{Outcome: "failed", Warning: "The hub could not verify the account."}
	}
	var selected *authFile
	for _, account := range accounts {
		if account.Id == accountId && account.Provider == "codex" && !account.IsDisabled {
			selected = &account
			break
		}
	}
	if selected == nil {
		return ResetResult{Outcome: "failed", Warning: "The Codex hub account is missing or disabled."}
	}
	identity := selected.Id
	if selected.Identity != nil && selected.Identity.AccountId != "" {
		identity = selected.Identity.AccountId
	}
	operationId := CreditRedeemRequestId(identity, creditId)
	data, err := hub.apiCall(ctx, config, *selected, "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume", map[string]string{"redeem_request_id": operationId, "credit_id": creditId})
	result := ResetResult{OperationId: operationId, Outcome: "unknown", Warning: "The reset result is unknown. Refresh limits before deciding whether to retry explicitly."}
	if err != nil {
		return result
	}
	var response struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(data, &response) != nil {
		return result
	}
	outcomes := map[string]string{"reset": "reset", "nothing_to_reset": "nothingToReset", "no_credit": "noCredit", "already_redeemed": "alreadyRedeemed"}
	outcome, exists := outcomes[response.Code]
	if !exists {
		return result
	}
	result.Outcome, result.Warning = outcome, ""
	if outcome == "reset" || outcome == "alreadyRedeemed" {
		if _, err := hub.management(ctx, config, "reset-quota", map[string]string{"auth_index": selected.Index}); err != nil {
			result.Warning = "Credit redeemed, but the hub cooldown could not be cleared. Routing may resume after its cooldown expires."
		}
	}
	return result
}

// ConsumeCodex verifies the selected account and any known workspace before sending a stable idempotency key.
// Legacy observations without a workspace retain email verification; ambiguous spends are never replayed automatically.
func ConsumeCodex(ctx context.Context, options NativeOptions, operationId string) ResetResult {
	result := ResetResult{Outcome: "failed", OperationId: operationId}
	if !options.IsEnabled || options.IsManagedAuth || strings.TrimSpace(operationId) == "" || strings.TrimSpace(options.ExpectedAccountEmail) == "" {
		result.Warning = "The native reset target is unavailable."
		return result
	}
	deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	session, err := openCodex(deadline, options)
	if err != nil {
		result.Warning = "Codex could not start the reset operation."
		return result
	}
	defer session.close()
	if session.initialize(deadline, options.Version) != nil {
		result.Warning = "Codex could not initialize the reset operation."
		return result
	}
	identity, err := session.request(deadline, 2, "account/read", map[string]any{})
	var account struct {
		Account *struct {
			Type  string `json:"type"`
			Email string `json:"email"`
		} `json:"account"`
	}
	if err != nil || json.Unmarshal(identity, &account) != nil || account.Account == nil || account.Account.Type != "chatgpt" {
		result.Warning = "Codex could not verify a subscription account."
		return result
	}
	if !strings.EqualFold(strings.TrimSpace(account.Account.Email), strings.TrimSpace(options.ExpectedAccountEmail)) {
		result.Warning = "The signed-in account changed. Refresh limits before resetting."
		return result
	}
	requestId := 3
	if expected := strings.TrimSpace(options.ExpectedWorkspaceId); expected != "" {
		data, err := session.request(deadline, requestId, "account/rateLimits/read", nil)
		var current struct {
			AccountId string `json:"accountId"`
		}
		if err != nil || json.Unmarshal(data, &current) != nil || strings.TrimSpace(current.AccountId) != expected {
			result.Warning = "The signed-in workspace could not be verified. Refresh limits before resetting."
			return result
		}
		requestId++
	}
	data, err := session.request(deadline, requestId, "account/rateLimitResetCredit/consume", map[string]string{"idempotencyKey": operationId})
	result.Outcome, result.Warning = "unknown", "The reset result is unknown. Refresh limits before deciding whether to retry explicitly."
	if err != nil {
		return result
	}
	var response struct {
		Outcome string `json:"outcome"`
	}
	if json.Unmarshal(data, &response) != nil {
		return result
	}
	switch response.Outcome {
	case "reset", "nothingToReset", "noCredit", "alreadyRedeemed":
		result.Outcome, result.Warning = response.Outcome, ""
	default:
		return result
	}
	if result.Outcome == "reset" || result.Outcome == "alreadyRedeemed" {
		data, err = session.request(deadline, requestId+1, "account/rateLimits/read", nil)
		if err == nil {
			_, err = MapCodex(data, checked(time.Now()))
		}
		if err != nil {
			result.Warning = "Credit redeemed, but refreshed limits could not be confirmed."
		}
	}
	return result
}

// Gate serializes refresh and redemption against the same current configuration.
type Gate struct {
	mutex   sync.Mutex
	locks   map[string]chan struct{}
	results map[string]ResetResult
}

func (gate *Gate) acquire(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	gate.mutex.Lock()
	if gate.locks == nil {
		gate.locks = map[string]chan struct{}{}
		gate.results = map[string]ResetResult{}
	}
	lock := gate.locks[key]
	if lock == nil {
		lock = make(chan struct{}, 1)
		gate.locks[key] = lock
	}
	gate.mutex.Unlock()
	select {
	case lock <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-lock
			return nil, err
		}
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// WithRefresh resolves configuration inside the acquired gate, never from a stale queued capture.
func (gate *Gate) WithRefresh(ctx context.Context, key string, readCurrent func() error) error {
	release, err := gate.acquire(ctx, key)
	if err != nil {
		return err
	}
	defer release()
	return readCurrent()
}

// Reset caches successful outcomes and requires an explicit decision before retrying a known or unknown failure.
func (gate *Gate) Reset(ctx context.Context, key, operationId string, isExplicitRetry bool, execute func() ResetResult) (ResetResult, error) {
	if key == "" || operationId == "" {
		return ResetResult{}, errors.New("missing reset identity")
	}
	release, err := gate.acquire(ctx, key)
	if err != nil {
		return ResetResult{}, err
	}
	defer release()
	identity := key + "\x00" + operationId
	gate.mutex.Lock()
	previous, exists := gate.results[identity]
	gate.mutex.Unlock()
	if exists && (previous.Outcome != "failed" && previous.Outcome != "unknown" || !isExplicitRetry) {
		return previous, nil
	}
	result := execute()
	result.OperationId = operationId
	gate.mutex.Lock()
	gate.results[identity] = result
	gate.mutex.Unlock()
	return result, nil
}
