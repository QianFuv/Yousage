package quota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type hubFixture struct {
	config           HubConfig
	client           HubClient
	isCreditFailed   atomic.Bool
	isCooldownFailed atomic.Bool
	isDisabled       atomic.Bool
	isConsumeBroken  atomic.Bool
	consumes         atomic.Int32
	cooldowns        atomic.Int32
	mutex            sync.Mutex
	requestIds       []string
}

func newHubFixture(t *testing.T) *hubFixture {
	t.Helper()
	fixture := &hubFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer owned-management" {
			t.Error("management auth")
		}
		respond := func(value any) {
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(value)
		}
		switch request.URL.Path {
		case "/v0/management/auth-files":
			respond(map[string]any{"files": []any{map[string]any{"id": "codex.json", "auth_index": "a", "provider": "codex", "email": "same@example.invalid", "disabled": fixture.isDisabled.Load(), "id_token": map[string]string{"chatgpt_account_id": "account-a", "chatgpt_plan_type": "pro"}}, map[string]string{"id": "claude.json", "auth_index": "b", "provider": "claude"}, map[string]string{"id": "ignored", "auth_index": "c", "provider": "grok"}}})
		case "/v0/management/api-call":
			var call struct {
				Index  string            `json:"auth_index"`
				Method string            `json:"method"`
				Url    string            `json:"url"`
				Header map[string]string `json:"header"`
				Data   *string           `json:"data"`
			}
			if json.NewDecoder(request.Body).Decode(&call) != nil || call.Header["Authorization"] != "Bearer $TOKEN$" {
				t.Error("proxy placeholder")
			}
			body := ""
			switch {
			case strings.HasSuffix(call.Url, "/consume"):
				fixture.consumes.Add(1)
				if call.Method != "POST" || call.Data == nil || call.Header["Chatgpt-Account-Id"] != "account-a" {
					t.Error("consume protocol")
					return
				}
				var input map[string]string
				_ = json.Unmarshal([]byte(*call.Data), &input)
				fixture.mutex.Lock()
				fixture.requestIds = append(fixture.requestIds, input["redeem_request_id"])
				fixture.mutex.Unlock()
				if input["credit_id"] != "early" {
					t.Error("credit identity")
				}
				body = `{"code":"reset"}`
				if fixture.isConsumeBroken.Load() {
					body = `{"code":"unknown-server-outcome"}`
				}
			case strings.HasSuffix(call.Url, "rate-limit-reset-credits"):
				if fixture.isCreditFailed.Load() {
					writer.WriteHeader(502)
					return
				}
				body = `{"credits":[{"id":"late","status":"available","reset_type":"codex_rate_limits","expires_at":"2099-02-01T00:00:00Z"},{"id":"early","status":"available","reset_type":"codex_rate_limits","expires_at":"2099-01-01T00:00:00Z"},{"id":"past","status":"available","reset_type":"codex_rate_limits","expires_at":"2000-01-01T00:00:00Z"}]}`
			case call.Index == "a":
				body = `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"reset_at":1800000000,"limit_window_seconds":18000},"secondary_window":{"used_percent":75,"limit_window_seconds":604800}}}`
			case call.Index == "b":
				if call.Header["anthropic-beta"] != "oauth-2025-04-20" {
					t.Error("Claude header")
				}
				body = `{"five_hour":{"utilization":10,"resets_at":null},"limits":[{"kind":"weekly_scoped","percent":80,"scope":{"model":{"display_name":"Fable"}}}]}`
			default:
				t.Error("unsupported account queried")
			}
			respond(map[string]any{"status_code": 200, "body": body})
		case "/v0/management/reset-quota":
			fixture.cooldowns.Add(1)
			if fixture.isCooldownFailed.Load() {
				writer.WriteHeader(502)
				return
			}
			respond(map[string]bool{"success": true})
		default:
			t.Error("base path incorrectly prefixed", request.URL.Path)
			writer.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	fixture.config = HubConfig{Id: "owned-hub", Label: "Fixture", Url: server.URL + "/ignored-prefix", CredentialId: "owned-key", IsEnabled: true}
	fixture.client = HubClient{Client: server.Client(), Key: func(context.Context, string) (string, error) { return "owned-management", nil }}
	return fixture
}
func TestHubAccountsCreditOutageAndDisabled(t *testing.T) {
	fixture := newHubFixture(t)
	result := fixture.client.Read(context.Background(), fixture.config)
	if result.Error != "" || len(result.Accounts) != 2 || result.Accounts[0].WorkspaceId != "account-a" || result.Accounts[1].WorkspaceId != "" || result.Accounts[0].Limits.Windows[0].UsedPercent != 25 || result.Accounts[0].Limits.ResetCredits.AvailableCount != 2 || result.Accounts[0].Limits.ResetCredits.NextCreditId != "early" || result.Accounts[1].Driver != "claudeAgent" || len(result.Accounts[1].Limits.Windows) != 2 {
		t.Fatal(result)
	}
	fixture.isCreditFailed.Store(true)
	result = fixture.client.Read(context.Background(), fixture.config)
	if result.Accounts[0].Limits.Unavailable != nil || result.Accounts[0].Limits.ResetCredits != nil || len(result.Accounts[0].Limits.Windows) != 2 {
		t.Fatal(result)
	}
	fixture.isDisabled.Store(true)
	result = fixture.client.Read(context.Background(), fixture.config)
	if len(result.Accounts) != 1 || result.Accounts[0].Driver != "claudeAgent" {
		t.Fatal(result)
	}
	reset := fixture.client.Consume(context.Background(), fixture.config, "codex.json", "early")
	if reset.Outcome != "failed" || fixture.consumes.Load() != 0 {
		t.Fatal(reset)
	}
}
func TestHubRedemptionIdentityCooldownWarningAndExplicitRetry(t *testing.T) {
	if CreditRedeemRequestId("account-a", "early") != "34ffbab7-c1e7-5057-9a5a-9dfbb75ed647" {
		t.Fatal("pinned Node UUIDv5 differential mismatch")
	}
	fixture := newHubFixture(t)
	fixture.isCooldownFailed.Store(true)
	result := fixture.client.Consume(context.Background(), fixture.config, "codex.json", "early")
	if result.Outcome != "reset" || result.Warning == "" || result.OperationId != CreditRedeemRequestId("account-a", "early") || fixture.cooldowns.Load() != 1 {
		t.Fatal(result)
	}
	fixture.isConsumeBroken.Store(true)
	var gate Gate
	operationId := CreditRedeemRequestId("account-a", "early")
	execute := func() ResetResult {
		return fixture.client.Consume(context.Background(), fixture.config, "codex.json", "early")
	}
	result, err := gate.Reset(context.Background(), "hub", operationId, false, execute)
	if err != nil || result.Outcome != "unknown" {
		t.Fatal(result, err)
	}
	_, _ = gate.Reset(context.Background(), "hub", operationId, false, execute)
	if fixture.consumes.Load() != 2 {
		t.Fatal("unknown reset was automatically replayed")
	}
	fixture.isConsumeBroken.Store(false)
	result, err = gate.Reset(context.Background(), "hub", operationId, true, execute)
	if err != nil || result.Outcome != "reset" || fixture.consumes.Load() != 3 {
		t.Fatal(result, err)
	}
	_, _ = gate.Reset(context.Background(), "hub", operationId, true, execute)
	if fixture.consumes.Load() != 3 {
		t.Fatal("confirmed reset was replayed")
	}
	for _, id := range fixture.requestIds {
		if id != operationId {
			t.Fatal("retry identity changed", id)
		}
	}
}
func TestGateRefreshReadsCurrentAfterResetAndCancellation(t *testing.T) {
	var gate Gate
	preCancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := gate.Reset(preCancelled, "idle", "operation", false, func() ResetResult { t.Fatal("cancelled idle reset executed"); return ResetResult{} }); err == nil {
		t.Fatal("pre-cancelled reset accepted")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan struct{})
	go func() {
		_, _ = gate.Reset(context.Background(), "source", "op", false, func() ResetResult { close(entered); <-release; return ResetResult{Outcome: "reset"} })
		close(finished)
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := gate.WithRefresh(ctx, "source", func() error { t.Error("cancelled waiter executed"); return nil }); err == nil {
		t.Fatal("wait ignored context")
	}
	close(release)
	<-finished
	isRemoved := true
	if err := gate.WithRefresh(context.Background(), "source", func() error {
		if !isRemoved {
			t.Fatal("stale config")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEqualAccountPoolFreshCreditsHubRouteAndSparseWindows(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	limits := func(at string, used float64) Limits {
		return makeLimits(at, []Window{{Id: "primary", Kind: "session", Label: "Session", UsedPercent: used, ResetsAt: checked(now.Add(150 * time.Minute)), WindowDurationMins: duration(300)}})
	}
	native := Candidate{Driver: "codex", EnvironmentId: "local", InstanceId: "first", DisplayName: "My Codex", Account: Account{Email: " SAME@example.invalid ", Limits: limits("2026-10-06T12:00:00.000Z", 40)}}
	native.Limits.ResetCredits = &ResetCredits{AvailableCount: 3}
	hub := Candidate{Driver: "codex", EnvironmentId: "remote", SourceId: "hub", AccountId: "a", Account: Account{Email: "same@example.invalid", Limits: limits("2026-10-06T11:00:00.000Z", 90)}}
	hub.Limits.ResetCredits = &ResetCredits{AvailableCount: 1, NextCreditId: "early"}
	other := Candidate{Driver: "codex", EnvironmentId: "other", InstanceId: "second", Account: Account{Email: "different@example.invalid", Limits: limits("2026-10-06T12:00:00.000Z", 80)}}
	other.Limits.Windows = append(other.Limits.Windows, Window{Id: "secondary", Kind: "weekly", Label: "Weekly", UsedPercent: 10})
	accounts := MergeAccounts([]Candidate{native, hub, other})
	if len(accounts) != 2 || accounts[0].Limits.Windows[0].UsedPercent != 40 || accounts[0].Limits.ResetCredits.AvailableCount != 3 || accounts[0].Redeem.SourceId != "hub" || len(accounts[0].Environments) != 1 {
		t.Fatal(accounts)
	}
	pools := CollectPools(accounts, now)
	if len(pools) != 1 || len(pools[0].Windows) != 2 || pools[0].Windows[0].UsedPercent != 60 || pools[0].Windows[0].Pace != "ahead" || len(pools[0].Windows[1].Columns) != 2 || pools[0].Windows[1].UsedPercent != 10 {
		t.Fatal(pools)
	}
	if pools[0].Windows[0].Resets[0].RestoresPercent+pools[0].Windows[0].Resets[1].RestoresPercent != 60 {
		t.Fatal("unequal restored shares")
	}
	missing := 0
	for _, column := range pools[0].Windows[1].Columns {
		if column.Window == nil {
			missing++
		}
	}
	if missing != 1 {
		t.Fatal("missing window lost fixed account column")
	}
}

func TestPoolFreshnessUsesInstantsAcrossOffsetAndPrecision(t *testing.T) {
	first := Candidate{Driver: "codex", EnvironmentId: "one", InstanceId: "first", Account: Account{Email: "same", Limits: makeLimits("2026-10-06T09:00:00Z", []Window{{Id: "primary", Kind: "session", UsedPercent: 10, ResetsAt: "2026-10-06T11:00:00+02:00"}})}}
	second := first
	second.EnvironmentId = "two"
	second.Limits = makeLimits("2026-10-06T17:00:00.001+08:00", []Window{{Id: "primary", Kind: "session", UsedPercent: 20, ResetsAt: "2026-10-06T09:00:00.001Z"}})
	merged := MergeAccounts([]Candidate{first, second})
	if len(merged) != 1 || merged[0].Limits.Windows[0].UsedPercent != 20 {
		t.Fatal(merged)
	}
	other := first
	other.Email = "different"
	other.EnvironmentId = "three"
	accounts := MergeAccounts([]Candidate{second, other})
	pools := CollectPools(accounts, time.Now())
	if pools[0].Accounts[0].Email != "different" {
		t.Fatal("reset order compared strings", pools)
	}
}

// TestPoolKeepsWorkspacesOfOneEmailApart counts each workspace's quota once without merging them.
func TestPoolKeepsWorkspacesOfOneEmailApart(t *testing.T) {
	limits := makeLimits("2026-10-06T12:00:00.000Z", []Window{{Id: "primary", Kind: "session", UsedPercent: 10}})
	personal := Candidate{Driver: "codex", EnvironmentId: "local", InstanceId: "personal", Account: Account{Email: "same@example.invalid", WorkspaceId: "personal", Limits: limits}}
	team := Candidate{Driver: "codex", EnvironmentId: "local", InstanceId: "team", Account: Account{Email: "same@example.invalid", WorkspaceId: "team", Limits: limits}}
	unnamed := Candidate{Driver: "codex", EnvironmentId: "remote", SourceId: "hub", AccountId: "a", Account: Account{Email: "same@example.invalid", Limits: limits}}
	accounts := MergeAccounts([]Candidate{personal, team, unnamed})
	if len(accounts) != 3 || accounts[0].Key == accounts[1].Key || accounts[2].Key != "hub:a" {
		t.Fatal("an ambiguous unnamed report joined a workspace", accounts)
	}
	accounts = MergeAccounts([]Candidate{personal, unnamed})
	if len(accounts) != 1 || accounts[0].Key != "codex:same@example.invalid:personal" {
		t.Fatal("an unnamed report did not join the only known workspace", accounts)
	}
}

// TestCodexPoolsWeeklySlotsByKind pools a weekly-only plan's primary slot with other plans' weekly slot.
func TestCodexPoolsWeeklySlotsByKind(t *testing.T) {
	weeklyOnly := Candidate{Driver: "codex", EnvironmentId: "local", InstanceId: "weekly", Account: Account{Email: "weekly@example.invalid", Limits: makeLimits("2026-10-06T12:00:00.000Z", []Window{{Id: "primary", Kind: "weekly", Label: "Weekly", UsedPercent: 20}})}}
	standard := Candidate{Driver: "codex", EnvironmentId: "local", InstanceId: "standard", Account: Account{Email: "standard@example.invalid", Limits: makeLimits("2026-10-06T12:00:00.000Z", []Window{{Id: "primary", Kind: "session", Label: "Session", UsedPercent: 50}, {Id: "secondary", Kind: "weekly", Label: "Weekly", UsedPercent: 40}})}}
	pools := CollectPools(MergeAccounts([]Candidate{weeklyOnly, standard}), time.Now())
	if len(pools) != 1 || len(pools[0].Windows) != 2 {
		t.Fatal("weekly slots were not pooled by kind", pools)
	}
	weekly := pools[0].Windows[1]
	if weekly.Id != "weekly" || weekly.UsedPercent != 30 || weekly.Columns[0].Window == nil || weekly.Columns[1].Window == nil {
		t.Fatal("weekly pool missed a member", weekly)
	}
	twoWeekly := Candidate{Driver: "codex", EnvironmentId: "local", InstanceId: "double", Account: Account{Email: "double@example.invalid", Limits: makeLimits("2026-10-06T12:00:00.000Z", []Window{{Id: "primary", Kind: "weekly", UsedPercent: 10}, {Id: "secondary", Kind: "weekly", UsedPercent: 90}})}}
	pools = CollectPools(MergeAccounts([]Candidate{twoWeekly}), time.Now())
	if len(pools[0].Windows) != 2 || pools[0].Windows[1].Id != "weekly:secondary" {
		t.Fatal("an account's second weekly window was pooled with its first", pools)
	}
}
