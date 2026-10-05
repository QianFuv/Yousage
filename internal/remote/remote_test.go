package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/secrets"

	"github.com/coder/websocket"
)

type memorySecrets struct {
	mutex  sync.Mutex
	values map[string]string
}

func (store *memorySecrets) Get(ctx context.Context, id string) (string, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	value := store.values[id]
	if value == "" {
		return "", secrets.ErrMissing
	}
	return value, nil
}
func (store *memorySecrets) Put(ctx context.Context, id, value string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.values[id] = value
	return nil
}
func (store *memorySecrets) Delete(ctx context.Context, id string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.values, id)
	return nil
}
func respond(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
func success(id string, value any) any {
	return map[string]any{"_tag": "Exit", "requestId": id, "exit": map[string]any{"_tag": "Success", "value": value}}
}

type frame struct {
	Tag       string          `json:"_tag"`
	Id        string          `json:"id"`
	Method    string          `json:"tag"`
	RequestId string          `json:"requestId"`
	Payload   json.RawMessage `json:"payload"`
	Headers   []any           `json:"headers"`
}
type serverFixture struct {
	server     *httptest.Server
	connector  Connector
	connection Connection
	exchanges  atomic.Int32
	isReadOnly bool
	protocol   int
	handler    func(context.Context, *websocket.Conn, frame)
}

func newServer(t *testing.T, handler func(context.Context, *websocket.Conn, frame)) *serverFixture {
	t.Helper()
	fixture := &serverFixture{protocol: 2, handler: handler}
	store := &memorySecrets{values: map[string]string{"fixture": "fixture-secret"}}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/t3/environment":
			respond(writer, Environment{Id: "fixture-environment", Label: "Fixture", Protocol: fixture.protocol, Capabilities: map[string]bool{"connectionProbe": true, "usagePriceOverrides": true, "usageModelAliases": true}})
		case "/oauth/token":
			fixture.exchanges.Add(1)
			_ = request.ParseForm()
			if request.Form.Get("subject_token") != "owned-bootstrap" || request.Form.Get("subject_token_type") != "urn:t3:params:oauth:token-type:environment-bootstrap" {
				t.Error("invalid exchange")
			}
			respond(writer, map[string]any{"access_token": "fixture-secret", "token_type": "Bearer", "issued_token_type": "urn:ietf:params:oauth:token-type:access_token", "expires_in": 3600, "scope": "orchestration:read orchestration:operate"})
		case "/api/auth/session":
			if request.Header.Get("Authorization") != "Bearer fixture-secret" {
				t.Error("missing bearer")
			}
			scopes := []string{"orchestration:read"}
			if !fixture.isReadOnly {
				scopes = append(scopes, "orchestration:operate")
			}
			respond(writer, Authorization{IsAuthenticated: true, Scopes: scopes, Auth: &AuthDescriptor{Policy: "remote-reachable", BootstrapMethods: []string{"one-time-token"}, SessionMethods: []string{"bearer-access-token"}, SessionCookieName: "fixture-cookie"}})
		case "/api/auth/websocket-ticket":
			if request.Header.Get("Authorization") != "Bearer fixture-secret" {
				t.Error("missing ticket bearer")
			}
			respond(writer, map[string]string{"ticket": "owned-ticket", "expiresAt": time.Now().Add(time.Minute).UTC().Format(time.RFC3339)})
		case "/ws":
			if request.URL.Query().Get("wsTicket") != "owned-ticket" || request.URL.Query().Get("orchestrationProtocol") != "2" || strings.Contains(request.URL.String(), "fixture-secret") || request.Header.Get("Authorization") != "" {
				t.Error("websocket credentials contract")
			}
			socket, err := websocket.Accept(writer, request, nil)
			if err != nil {
				return
			}
			defer socket.CloseNow()
			ctx, cancel := context.WithCancel(request.Context())
			defer cancel()
			for {
				_, data, err := socket.Read(ctx)
				if err != nil {
					return
				}
				var message frame
				if json.Unmarshal(data, &message) != nil {
					t.Error("invalid frame")
					return
				}
				if message.Tag == "Ping" {
					writeFrame(t, ctx, socket, map[string]string{"_tag": "Pong"})
					continue
				}
				if fixture.handler != nil {
					fixture.handler(ctx, socket, message)
				}
			}
		default:
			http.NotFound(writer, request)
		}
	}))
	fixture.connector = Connector{HttpClient: fixture.server.Client(), Secrets: store}
	fixture.connection = Connection{Url: fixture.server.URL, EnvironmentId: "fixture-environment", CredentialId: "fixture"}
	t.Cleanup(fixture.server.Close)
	return fixture
}
func writeFrame(t *testing.T, ctx context.Context, socket *websocket.Conn, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Error(err)
		return
	}
	if err = socket.Write(ctx, websocket.MessageText, data); err != nil && ctx.Err() == nil {
		t.Error(err)
	}
}
func connectFixture(t *testing.T, fixture *serverFixture) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := fixture.connector.Connect(ctx, fixture.connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func TestIdentityAndProtocolBeforeCredentialExchange(t *testing.T) {
	fixture := newServer(t, nil)
	connection := fixture.connection
	connection.EnvironmentId = "different"
	if _, err := fixture.connector.Pair(context.Background(), connection, "owned-bootstrap", false); err == nil || fixture.exchanges.Load() != 0 {
		t.Fatal(err, fixture.exchanges.Load())
	}
	connection = fixture.connection
	fixture.protocol = 1
	if _, err := fixture.connector.Pair(context.Background(), connection, "owned-bootstrap", false); err == nil || fixture.exchanges.Load() != 0 {
		t.Fatal(err, fixture.exchanges.Load())
	}
	fixture.protocol = 2
	if _, err := fixture.connector.Pair(context.Background(), connection, "owned-bootstrap", true); err != nil || fixture.exchanges.Load() != 1 {
		t.Fatal(err, fixture.exchanges.Load())
	}
	for _, raw := range []string{"http://remote.example", "https://user:secret@remote.example", "https://remote.example/ws?token=secret"} {
		if _, err := origin(raw); err == nil {
			t.Fatal("unsafe origin accepted", raw)
		}
	}
	connection.EnvironmentId = ""
	if _, err := fixture.connector.Connect(context.Background(), connection); err == nil {
		t.Fatal("restore without expected identity")
	}
}

func TestCancelledRequestKeepsSharedConnectionUsable(t *testing.T) {
	var calls atomic.Int32
	fixture := newServer(t, func(ctx context.Context, socket *websocket.Conn, message frame) {
		if message.Tag == "Request" {
			calls.Add(1)
			writeFrame(t, ctx, socket, success(message.Id, true))
		}
	})
	client := connectFixture(t, fixture)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Call(ctx, "server.updateSettings", map[string]any{}, true)
	var failure *CallError
	if !errors.As(err, &failure) || failure.IsOutcomeUnknown {
		t.Fatal(err)
	}
	deadline, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := client.Probe(deadline); err != nil || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
}

func TestMalformedWriteReplyIsUnknown(t *testing.T) {
	for _, tag := range []string{"Chunk", "invalid", "Success"} {
		fixture := newServer(t, func(ctx context.Context, socket *websocket.Conn, message frame) {
			if message.Tag != "Request" {
				return
			}
			response := map[string]any{"_tag": "Exit", "requestId": message.Id, "exit": map[string]any{"_tag": tag}}
			if tag == "Chunk" {
				response = map[string]any{"_tag": "Chunk", "requestId": message.Id, "values": []any{}}
			}
			writeFrame(t, ctx, socket, response)
		})
		client := connectFixture(t, fixture)
		_, err := client.Call(context.Background(), "server.updateSettings", map[string]any{}, true)
		var failure *CallError
		if !errors.As(err, &failure) || !failure.IsOutcomeUnknown {
			t.Fatal(tag, err)
		}
	}
}

func TestSlowDestinationDoesNotBlockHealthySave(t *testing.T) {
	healthySaved := make(chan struct{}, 1)
	slow := newServer(t, func(context.Context, *websocket.Conn, frame) {})
	healthy := newServer(t, func(ctx context.Context, socket *websocket.Conn, message frame) {
		if message.Tag == "Request" {
			healthySaved <- struct{}{}
			writeFrame(t, ctx, socket, success(message.Id, true))
		}
	})
	slowClient, healthyClient := connectFixture(t, slow), connectFixture(t, healthy)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan []SaveOutcome, 1)
	go func() {
		done <- SaveSettings(ctx, []SaveTarget{{"slow", slowClient}, {"healthy", healthyClient}}, SettingsPatch{})
	}()
	select {
	case <-healthySaved:
	case <-ctx.Done():
		t.Fatal("healthy save blocked by slow target")
	}
	outcomes := <-done
	if !outcomes[1].IsSaved || outcomes[0].IsSaved || !outcomes[0].IsOutcomeUnknown {
		t.Fatal(outcomes)
	}
}

func TestObjectBatchCorrelationAndPingPong(t *testing.T) {
	pong := make(chan struct{}, 1)
	fixture := newServer(t, func(ctx context.Context, socket *websocket.Conn, message frame) {
		if message.Tag == "Pong" {
			pong <- struct{}{}
			return
		}
		if message.Tag != "Request" {
			return
		}
		if message.Method != "server.probe" || message.Headers == nil {
			t.Error("wrong Effect request")
		}
		writeFrame(t, ctx, socket, []any{map[string]string{"_tag": "Ping"}, success("unmatched", false), success(message.Id, map[string]bool{"ok": true})})
	})
	client := connectFixture(t, fixture)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pong:
	case <-ctx.Done():
		t.Fatal("Pong missing")
	}
}

func TestSubscriptionAckAfterConsumptionAndInterrupt(t *testing.T) {
	ack := make(chan bool, 1)
	interrupt := make(chan struct{}, 1)
	hasConsumed := atomic.Bool{}
	fixture := newServer(t, func(ctx context.Context, socket *websocket.Conn, message frame) {
		switch message.Tag {
		case "Request":
			writeFrame(t, ctx, socket, map[string]any{"_tag": "Chunk", "requestId": message.Id, "values": []any{map[string]any{"version": 1, "type": "usageLimitSourcesUpdated", "payload": map[string]any{"sources": []any{}}}}})
		case "Ack":
			ack <- hasConsumed.Load()
		case "Interrupt":
			interrupt <- struct{}{}
		}
	})
	client := connectFixture(t, fixture)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- client.Subscribe(ctx, func(value json.RawMessage) error {
			if !strings.Contains(string(value), "usageLimitSourcesUpdated") {
				return errors.New("wrong event")
			}
			hasConsumed.Store(true)
			return nil
		})
	}()
	select {
	case consumed := <-ack:
		if !consumed {
			t.Fatal("ack before consume")
		}
	case <-time.After(time.Second):
		t.Fatal("Ack missing")
	}
	cancel()
	if err := <-finished; err != context.Canceled {
		t.Fatal(err)
	}
	select {
	case <-interrupt:
	case <-time.After(time.Second):
		t.Fatal("Interrupt missing")
	}
}

func TestReadOnlyFailuresAndUnknownWriteOutcomeNoReplay(t *testing.T) {
	var calls atomic.Int32
	fixture := newServer(t, func(ctx context.Context, socket *websocket.Conn, message frame) {
		if message.Tag != "Request" {
			return
		}
		calls.Add(1)
		_ = socket.CloseNow()
	})
	client := connectFixture(t, fixture)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	price := domain.PriceOverride{InputCostPerMillionTokens: 2, OutputCostPerMillionTokens: 8}
	_, err := client.UpdateSettings(ctx, SettingsPatch{Prices: map[string]*domain.PriceOverride{"fixture": &price}})
	var failure *CallError
	if !errors.As(err, &failure) || !failure.IsOutcomeUnknown || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
	_, _ = client.UpdateSettings(ctx, SettingsPatch{Prices: map[string]*domain.PriceOverride{"fixture": &price}})
	if calls.Load() != 1 {
		t.Fatal("replayed offline write")
	}
	readOnly := newServer(t, nil)
	readOnly.isReadOnly = true
	reader := connectFixture(t, readOnly)
	_, err = reader.UpdateSettings(ctx, SettingsPatch{Prices: map[string]*domain.PriceOverride{"fixture": &price}})
	if !errors.As(err, &failure) || !failure.IsAuthorization || failure.IsOutcomeUnknown {
		t.Fatal(err)
	}
}

func TestAtomicPatchAndRetryOnlyFailedDestination(t *testing.T) {
	var goodCalls, badCalls atomic.Int32
	good := newServer(t, func(ctx context.Context, socket *websocket.Conn, message frame) {
		if message.Tag != "Request" {
			return
		}
		goodCalls.Add(1)
		if message.Method != "server.updateSettings" || !strings.Contains(string(message.Payload), `"fixture":null`) || !strings.Contains(string(message.Payload), `"usageModelAliases"`) {
			t.Error("patch lost deletes or aliases")
		}
		writeFrame(t, ctx, socket, success(message.Id, map[string]bool{"saved": true}))
	})
	bad := newServer(t, func(ctx context.Context, socket *websocket.Conn, message frame) {
		if message.Tag != "Request" {
			return
		}
		badCalls.Add(1)
		writeFrame(t, ctx, socket, map[string]any{"_tag": "Exit", "requestId": message.Id, "exit": map[string]any{"_tag": "Failure", "cause": []any{map[string]any{"_tag": "Fail", "error": map[string]string{"_tag": "EnvironmentAuthorizationError", "message": "sensitive error details"}}}}})
	})
	goodClient, badClient := connectFixture(t, good), connectFixture(t, bad)
	target := "target"
	patch := SettingsPatch{Prices: map[string]*domain.PriceOverride{"fixture": nil}, Aliases: map[string]*string{"fixture": &target}}
	outcomes := SaveSettings(context.Background(), []SaveTarget{{"good", goodClient}, {"bad", badClient}}, patch)
	if !outcomes[0].IsSaved || outcomes[1].IsSaved || outcomes[1].Error != "environment grants read-only access" {
		t.Fatal(outcomes)
	}
	_ = SaveSettings(context.Background(), []SaveTarget{{"bad", badClient}}, patch)
	if goodCalls.Load() != 1 || badCalls.Load() != 2 {
		t.Fatal(goodCalls.Load(), badCalls.Load())
	}
}

func summaryFixture() map[string]any {
	return map[string]any{"contractVersion": 6, "readAt": "2026-10-06T09:00:00Z", "timeZone": "Asia/Shanghai", "sinceDay": "2026-10-01", "untilDay": "2026-10-06", "buckets": []any{map[string]any{"day": "2026-10-06", "provider": "claude", "model": "fixture", "sourcePath": "/owned", "totals": domain.Tokens{UncachedInputTokens: 100, CachedInputTokens: 20, CacheCreationTokens: 10, OutputTokens: 50, ReasoningTokens: 15}, "costUsd": 0.001, "cacheSavingsUsd": 0.00002, "costSource": "modelPriced", "records": 2, "unpricedRecords": 0, "sessions": 1}}, "sources": []any{Source{Fingerprint: domain.SourceFingerprint{HostId: "fixture", Provider: domain.Claude, ResolvedHomePath: "/owned", VolumeId: "1:42"}, Status: "ok", ScannedFiles: 1, DistinctSessions: 1}}, "pricing": Pricing{Status: "cached", Source: "fixture", KnownModels: 1}, "scanDurationMs": 1}
}
func TestSummaryVersionsForwardCompatibleArraysAndWindow(t *testing.T) {
	for _, version := range []int{4, 5, 6} {
		value := summaryFixture()
		value["contractVersion"] = version
		bucket := value["buckets"].([]any)[0].(map[string]any)
		unknown := map[string]any{}
		for key, item := range bucket {
			unknown[key] = item
		}
		unknown["provider"] = "future-provider"
		invalid := map[string]any{}
		for key, item := range bucket {
			invalid[key] = item
		}
		invalid["costUsd"] = nil
		value["buckets"] = append(value["buckets"].([]any), unknown, invalid)
		value["sources"] = append(value["sources"].([]any), map[string]any{"fingerprint": nil})
		data, _ := json.Marshal(value)
		result, err := DecodeSummary(data)
		if err != nil || len(result.Buckets) != 1 || result.Buckets[0].Totals.Total() != 180 || len(result.Sources) != 1 {
			t.Fatal(result, err)
		}
	}
	for _, version := range []int{3, 7} {
		value := summaryFixture()
		value["contractVersion"] = version
		data, _ := json.Marshal(value)
		if _, err := DecodeSummary(data); err == nil {
			t.Fatal(version)
		}
	}
	fixture := newServer(t, func(ctx context.Context, socket *websocket.Conn, message frame) {
		if message.Tag != "Request" {
			return
		}
		if message.Method != "server.getUsageSummary" || !strings.Contains(string(message.Payload), `"sinceTime":"1970-01-01T00:00:01Z"`) {
			t.Error("hour window contract", string(message.Payload))
		}
		writeFrame(t, ctx, socket, success(message.Id, summaryFixture()))
	})
	client := connectFixture(t, fixture)
	result, err := client.Summary(context.Background(), accounting.Window{TimeZone: "UTC", SinceDay: "2026-10-01", UntilDay: "2026-10-06", Resolution: "hour", SinceTimeMs: 1000, UntilTimeMs: 2000})
	if err != nil || len(result.Buckets) != 1 {
		t.Fatal(result, err)
	}
}

func TestHttpAuthProtocolAndRedirectFailures(t *testing.T) {
	for _, status := range []int{401, 403, 426} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(status)
			respond(writer, map[string]string{"message": "fixture-secret should not escape"})
		}))
		connector := Connector{HttpClient: server.Client()}
		_, err := connector.Discover(context.Background(), Connection{Url: server.URL})
		server.Close()
		if err == nil || strings.Contains(err.Error(), "fixture-secret") {
			t.Fatal(status, err)
		}
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Redirect(writer, request, "https://another.example", 302)
	}))
	defer server.Close()
	_, err := (Connector{HttpClient: server.Client()}).Discover(context.Background(), Connection{Url: server.URL})
	if err == nil || requests.Load() != 1 {
		t.Fatal(err, requests.Load())
	}
}

func TestPendingCallCancellationInterruptAndCapabilityCheck(t *testing.T) {
	requested, interrupted := make(chan struct{}, 1), make(chan struct{}, 1)
	fixture := newServer(t, func(ctx context.Context, socket *websocket.Conn, message frame) {
		if message.Tag == "Request" {
			requested <- struct{}{}
		}
		if message.Tag == "Interrupt" {
			interrupted <- struct{}{}
		}
	})
	client := connectFixture(t, fixture)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := client.Call(ctx, "server.getSettings", map[string]any{}, false); finished <- err }()
	<-requested
	cancel()
	if err := <-finished; err == nil {
		t.Fatal("cancelled read succeeded")
	}
	select {
	case <-interrupted:
	case <-time.After(time.Second):
		t.Fatal("cancel interrupt missing")
	}
	delete(client.Environment.Capabilities, "usageModelAliases")
	target := "target"
	if _, err := client.UpdateSettings(context.Background(), SettingsPatch{Aliases: map[string]*string{"fixture": &target}}); err == nil || !strings.Contains(err.Error(), "update server") {
		t.Fatal(err)
	}
}
