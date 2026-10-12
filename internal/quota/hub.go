package quota

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type HubConfig struct {
	Id           string `json:"id"`
	Label        string `json:"label"`
	Url          string `json:"url"`
	CredentialId string `json:"credentialId"`
	IsEnabled    bool   `json:"enabled"`
}
type HubAccount struct {
	Id     string `json:"id"`
	Driver string `json:"driver"`
	Account
}
type HubSnapshot struct {
	Id        string       `json:"id"`
	Kind      string       `json:"kind"`
	Label     string       `json:"label"`
	CheckedAt string       `json:"checkedAt"`
	Accounts  []HubAccount `json:"accounts"`
	Error     string       `json:"error,omitempty"`
}
type authFile struct {
	Id         string `json:"id"`
	Index      string `json:"auth_index"`
	Provider   string `json:"provider"`
	Email      string `json:"email"`
	IsDisabled bool   `json:"disabled"`
	Identity   *struct {
		AccountId string `json:"chatgpt_account_id"`
		Plan      string `json:"chatgpt_plan_type"`
	} `json:"id_token"`
}

// workspaceId names the ChatGPT workspace whose quota this login draws on.
func (account authFile) workspaceId() string {
	if account.Identity == nil {
		return ""
	}
	return strings.TrimSpace(account.Identity.AccountId)
}

type HubClient struct {
	Client *http.Client
	Key    func(context.Context, string) (string, error)
}

func (hub HubClient) management(ctx context.Context, config HubConfig, path string, body any) (json.RawMessage, error) {
	base, err := url.Parse(config.Url)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil {
		return nil, errors.New("The hub URL is not valid.")
	}
	base.Path, base.RawPath, base.RawQuery, base.Fragment = "/v0/management/"+path, "", "", ""
	key := ""
	if hub.Key != nil {
		key, err = hub.Key(ctx, config.CredentialId)
		if err != nil {
			return nil, errors.New("The hub credential is unavailable.")
		}
	}
	method := http.MethodGet
	var input io.Reader
	if body != nil {
		data, marshalError := json.Marshal(body)
		if marshalError != nil {
			return nil, errors.New("The hub request is invalid.")
		}
		input, method = bytes.NewReader(data), http.MethodPost
	}
	deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(deadline, method, base.String(), input)
	if err != nil {
		return nil, errors.New("The hub request is invalid.")
	}
	request.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := http.Client{}
	if hub.Client != nil {
		client = *hub.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("redirect disabled") }
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("The hub management request failed.")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("The hub management request failed.")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 || !json.Valid(data) {
		return nil, errors.New("The hub returned invalid JSON.")
	}
	return data, nil
}
func (hub HubClient) authFiles(ctx context.Context, config HubConfig) ([]authFile, error) {
	data, err := hub.management(ctx, config, "auth-files", nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Files []json.RawMessage `json:"files"`
	}
	if !validObject(data, nil) || json.Unmarshal(data, &response) != nil || response.Files == nil {
		return nil, errors.New("The hub could not list accounts.")
	}
	accounts := []authFile{}
	for _, raw := range response.Files {
		if !validObject(raw, map[string]string{"id": "string", "auth_index": "string", "provider": "string", "email": "string", "id_token": "object"}) {
			return nil, errors.New("The hub could not list accounts.")
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if fields["id"] == nil || fields["auth_index"] == nil || fields["provider"] == nil {
			return nil, errors.New("The hub could not list accounts.")
		}
		if identity, exists := fields["id_token"]; exists && !validObject(identity, map[string]string{"chatgpt_account_id": "string", "chatgpt_plan_type": "string"}) {
			return nil, errors.New("The hub could not list accounts.")
		}
		if disabled, exists := fields["disabled"]; exists && string(disabled) != "true" && string(disabled) != "false" {
			return nil, errors.New("The hub could not list accounts.")
		}
		var account authFile
		if json.Unmarshal(raw, &account) != nil {
			return nil, errors.New("The hub could not list accounts.")
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}
func (hub HubClient) apiCall(ctx context.Context, config HubConfig, account authFile, endpoint string, input any) (json.RawMessage, error) {
	header := map[string]string{"Authorization": "Bearer $TOKEN$"}
	if account.Provider == "codex" {
		header["Content-Type"], header["OpenAI-Beta"], header["Originator"] = "application/json", "codex-1", "Codex Desktop"
		if account.Identity != nil && account.Identity.AccountId != "" {
			header["Chatgpt-Account-Id"] = account.Identity.AccountId
		}
	} else {
		header["anthropic-beta"] = "oauth-2025-04-20"
	}
	request := map[string]any{"auth_index": account.Index, "method": "GET", "url": endpoint, "header": header}
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		request["method"], request["data"] = "POST", string(data)
	}
	data, err := hub.management(ctx, config, "api-call", request)
	if err != nil {
		return nil, err
	}
	var response struct {
		Status *float64 `json:"status_code"`
		Body   *string  `json:"body"`
	}
	if !validObject(data, map[string]string{"status_code": "number", "body": "string"}) || json.Unmarshal(data, &response) != nil || response.Status == nil || response.Body == nil {
		return nil, errors.New("The hub returned an unexpected provider response.")
	}
	if *response.Status < 200 || *response.Status >= 300 {
		return nil, errors.New("The provider refused the hub request.")
	}
	if !json.Valid([]byte(*response.Body)) {
		return nil, errors.New("The provider returned invalid JSON.")
	}
	return []byte(*response.Body), nil
}

func mapHubCodex(data json.RawMessage, checkedAt string) (Limits, string, error) {
	var response struct {
		Plan string          `json:"plan_type"`
		Rate json.RawMessage `json:"rate_limit"`
	}
	if !validObject(data, map[string]string{"plan_type": "string"}) || json.Unmarshal(data, &response) != nil || response.Rate == nil {
		return Limits{}, "", errors.New("invalid hub usage")
	}
	snapshot := map[string]any{"planType": response.Plan}
	if string(response.Rate) != "null" {
		if !validObject(response.Rate, nil) {
			return Limits{}, "", errors.New("invalid hub rate limits")
		}
		var windows map[string]json.RawMessage
		_ = json.Unmarshal(response.Rate, &windows)
		for _, position := range []string{"primary", "secondary"} {
			raw, exists := windows[position+"_window"]
			if !exists || string(raw) == "null" {
				continue
			}
			if !validObject(raw, map[string]string{"used_percent": "number", "limit_window_seconds": "number"}) {
				return Limits{}, "", errors.New("invalid hub window")
			}
			var window struct {
				Used    *float64 `json:"used_percent"`
				Reset   *float64 `json:"reset_at"`
				Seconds *float64 `json:"limit_window_seconds"`
			}
			if json.Unmarshal(raw, &window) != nil || window.Used == nil {
				return Limits{}, "", errors.New("invalid hub window")
			}
			mapped := map[string]any{"usedPercent": *window.Used, "resetsAt": window.Reset}
			if window.Seconds != nil {
				mapped["windowDurationMins"] = *window.Seconds / 60
			}
			snapshot[position] = mapped
		}
	}
	mapped, _ := json.Marshal(map[string]any{"rateLimits": snapshot})
	limits, err := MapCodex(mapped, checkedAt)
	return limits, response.Plan, err
}
func mapHubClaude(data json.RawMessage, checkedAt string) (Limits, error) {
	if !validObject(data, nil) {
		return Limits{}, errors.New("invalid hub Claude usage")
	}
	var values map[string]json.RawMessage
	_ = json.Unmarshal(data, &values)
	rates := map[string]any{}
	for _, id := range []string{"five_hour", "seven_day"} {
		raw, exists := values[id]
		if !exists || string(raw) == "null" {
			continue
		}
		if !validObject(raw, map[string]string{"utilization": "number"}) {
			return Limits{}, errors.New("invalid hub Claude window")
		}
		var window map[string]any
		_ = json.Unmarshal(raw, &window)
		if numeric(window["utilization"]) == nil {
			return Limits{}, errors.New("invalid hub Claude percentage")
		}
		reset, exists := window["resets_at"]
		if !exists {
			return Limits{}, errors.New("invalid hub Claude reset")
		}
		if _, ok := reset.(string); reset != nil && !ok {
			return Limits{}, errors.New("invalid hub Claude reset")
		}
		rates[id] = window
	}
	var scoped []map[string]any
	if raw, exists := values["limits"]; exists {
		if json.Unmarshal(raw, &scoped) != nil || scoped == nil {
			return Limits{}, errors.New("invalid hub scoped limits")
		}
	}
	modelScoped := []map[string]any{}
	for _, entry := range scoped {
		if entry == nil {
			return Limits{}, errors.New("invalid hub scoped limit")
		}
		kind, ok := entry["kind"].(string)
		if !ok {
			return Limits{}, errors.New("invalid hub scoped kind")
		}
		used := numeric(entry["percent"])
		if value := entry["percent"]; value != nil && used == nil {
			return Limits{}, errors.New("invalid hub scoped percentage")
		}
		scope, _ := entry["scope"].(map[string]any)
		if entry["scope"] != nil && scope == nil {
			return Limits{}, errors.New("invalid hub scoped scope")
		}
		model, _ := scope["model"].(map[string]any)
		if scope["model"] != nil && model == nil {
			return Limits{}, errors.New("invalid hub scoped model")
		}
		name, hasName := model["display_name"].(string)
		if model != nil && !hasName {
			return Limits{}, errors.New("invalid hub model name")
		}
		if reset := entry["resets_at"]; reset != nil {
			if _, ok := reset.(string); !ok {
				return Limits{}, errors.New("invalid hub scoped reset")
			}
		}
		if kind == "weekly_scoped" && hasName && used != nil {
			modelScoped = append(modelScoped, map[string]any{"display_name": name, "utilization": *used, "resets_at": entry["resets_at"]})
		}
	}
	rates["model_scoped"] = modelScoped
	mapped, _ := json.Marshal(map[string]any{"rate_limits_available": true, "rate_limits": rates})
	limits, _, err := MapClaude(mapped, checkedAt)
	return limits, err
}
func (hub HubClient) credits(ctx context.Context, config HubConfig, account authFile) (*ResetCredits, error) {
	data, err := hub.apiCall(ctx, config, account, "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits", nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Credits []struct {
			Id      *string `json:"id"`
			Status  *string `json:"status"`
			Type    *string `json:"reset_type"`
			Expires *string `json:"expires_at"`
		} `json:"credits"`
	}
	if json.Unmarshal(data, &response) != nil || response.Credits == nil {
		return nil, errors.New("invalid credits")
	}
	type available struct{ id, expires string }
	selected := []available{}
	for _, credit := range response.Credits {
		if credit.Id == nil || credit.Status == nil || credit.Type == nil || credit.Expires == nil {
			return nil, errors.New("invalid credit")
		}
		expires := iso(*credit.Expires)
		if *credit.Status == "available" && *credit.Type == "codex_rate_limits" && expires > checked(time.Now()) {
			selected = append(selected, available{*credit.Id, expires})
		}
	}
	sort.SliceStable(selected, func(first, second int) bool { return selected[first].expires < selected[second].expires })
	result := &ResetCredits{AvailableCount: len(selected)}
	if len(selected) > 0 {
		result.NextCreditId, result.NextExpiresAt = selected[0].id, selected[0].expires
	}
	return result, nil
}
func (hub HubClient) readAccount(ctx context.Context, config HubConfig, account authFile) HubAccount {
	driver := "codex"
	if account.Provider == "claude" {
		driver = "claudeAgent"
	}
	result := HubAccount{Id: account.Id, Driver: driver, Account: Account{Email: account.Email, WorkspaceId: account.workspaceId(), Limits: unavailable(checked(time.Now()), "probeFailed", "The hub could not read this account's usage.")}}
	endpoint := "https://chatgpt.com/backend-api/wham/usage"
	if driver == "claudeAgent" {
		endpoint = "https://api.anthropic.com/api/oauth/usage"
	}
	data, err := hub.apiCall(ctx, config, account, endpoint, nil)
	if err != nil {
		return result
	}
	if driver == "claudeAgent" {
		limits, mapError := mapHubClaude(data, result.Limits.CheckedAt)
		if mapError == nil {
			result.Limits, result.Plan = limits, "Claude Subscription"
		}
		return result
	}
	limits, plan, err := mapHubCodex(data, result.Limits.CheckedAt)
	if err != nil {
		return result
	}
	if plan == "" && account.Identity != nil {
		plan = account.Identity.Plan
	}
	result.Plan = planLabel(plan)
	if credits, err := hub.credits(ctx, config, account); err == nil {
		limits.ResetCredits = credits
	}
	result.Limits = limits
	return result
}
func planLabel(plan string) string {
	if plan == "" {
		return "ChatGPT"
	}
	return "ChatGPT " + strings.ToUpper(plan[:1]) + plan[1:]
}

// Read retains each failed account independently and bounds account request concurrency to four.
func (hub HubClient) Read(ctx context.Context, config HubConfig) HubSnapshot {
	result := HubSnapshot{Id: config.Id, Kind: "cliproxy", Label: config.Label, CheckedAt: checked(time.Now()), Accounts: []HubAccount{}}
	if !config.IsEnabled {
		return result
	}
	accounts, err := hub.authFiles(ctx, config)
	if err != nil {
		result.Error = "The hub could not list accounts."
		return result
	}
	for _, account := range accounts {
		if !account.IsDisabled && (account.Provider == "codex" || account.Provider == "claude") {
			result.Accounts = append(result.Accounts, HubAccount{Id: account.Id})
		}
	}
	var workers sync.WaitGroup
	semaphore := make(chan struct{}, 4)
	index := 0
	for _, account := range accounts {
		if account.IsDisabled || (account.Provider != "codex" && account.Provider != "claude") {
			continue
		}
		position := index
		index++
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				driver := "codex"
				if account.Provider == "claude" {
					driver = "claudeAgent"
				}
				result.Accounts[position] = HubAccount{Id: account.Id, Driver: driver, Account: Account{Email: account.Email, WorkspaceId: account.workspaceId(), Limits: unavailable(checked(time.Now()), "probeFailed", "The hub could not read this account's usage.")}}
				return
			}
			result.Accounts[position] = hub.readAccount(ctx, config, account)
		}()
	}
	workers.Wait()
	return result
}

// CreditRedeemRequestId preserves UUIDv5 identity across explicit retries and environments.
func CreditRedeemRequestId(accountId, creditId string) string {
	namespace, _ := hex.DecodeString("6f1c2a9e2d4b4c1e9a7f3b8d5e0c1a42")
	hasher := sha1.New()
	_, _ = hasher.Write(namespace)
	_, _ = hasher.Write([]byte(accountId + ":" + creditId))
	value := hasher.Sum(nil)[:16]
	value[6] = (value[6] & 0x0f) | 0x50
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:])
}
