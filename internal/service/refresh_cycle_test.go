package service

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/refresh"
	"yousage/internal/remote"
)

// TestRefreshCycleScansHistoryOnceAndRetainsProbeFailure checks real persistence and cooldown notices.
func TestRefreshCycleScansHistoryOnceAndRetainsProbeFailure(t *testing.T) {
	service := cycleRefreshFixture(t)
	ctx := context.Background()
	var invalidations atomic.Int32
	service.Emit = func(name string, value any) {
		if name == "usage-invalidated" {
			invalidations.Add(1)
		}
	}
	_, before, err := service.sourceIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RefreshCycle(ctx, "UTC", refresh.Request{IsManual: true}); err != nil {
		t.Fatal(err)
	}
	_, after, err := service.sourceIndex(ctx)
	if err != nil || after != before+1 {
		t.Fatal("global cycle did not scan history exactly once", before, after, err)
	}
	service.mutex.Lock()
	hasFollowup := service.limitsFollowup != nil
	service.mutex.Unlock()
	if hasFollowup || invalidations.Load() != 1 {
		t.Fatal("cycle spawned a redundant followup or failed to invalidate once", invalidations.Load())
	}
	previous, _, err := service.quotas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	service.Environment["YOUSAGE_CODEX_SCENARIO"] = "rpc-error"
	if err := service.RefreshCycle(ctx, "UTC", refresh.Request{IsManual: true}); err == nil {
		t.Fatal("probe failure was hidden")
	}
	failed, _, err := service.quotas(ctx)
	if err != nil || failed.Error == "" || !reflect.DeepEqual(failed.Providers[0].Limits, previous.Providers[0].Limits) {
		t.Fatal("probe failure lost retained limits or notice", err)
	}
	if !strings.Contains(failed.Error, "JSON-RPC -32603; server") {
		t.Fatal("safe RPC diagnostics not retained")
	}
	requestsBefore, err := os.ReadFile(service.Environment["YOUSAGE_REQUEST_FILE"])
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RefreshCycle(ctx, "UTC", refresh.Request{IsBackground: true, IsAutoEnabled: true}); err == nil {
		t.Fatal("cooldown hid retained probe failure")
	}
	requestsAfter, err := os.ReadFile(service.Environment["YOUSAGE_REQUEST_FILE"])
	if err != nil || string(requestsAfter) != string(requestsBefore) {
		t.Fatal("cooldown did not protect the probe", err)
	}
	if invalidations.Load() != 3 {
		t.Fatal("partial cycle did not invalidate", invalidations.Load())
	}
	service.Environment["YOUSAGE_CODEX_SCENARIO"] = "success"
	if err := service.RefreshCycle(ctx, "UTC", refresh.Request{IsManual: true}); err != nil {
		t.Fatal("recovery retained failure", err)
	}
	recovered, _, err := service.quotas(ctx)
	if err != nil || recovered.Error != "" {
		t.Fatal("recovery did not clear probe notice", err)
	}
}

// TestConnectedHookAndPartialCycleRefreshEveryWindow verifies the real connection-ready hook,
// all four remote requests, independent limits work and final invalidation after failures.
func TestConnectedHookAndPartialCycleRefreshEveryWindow(t *testing.T) {
	service := testService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var summaryCalls, limitsCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/.well-known/t3/environment":
			if err := json.NewEncoder(writer).Encode(remote.Environment{Id: "remote-fixture", Label: "Remote", Protocol: 2, Capabilities: remote.Capabilities{}}); err != nil {
				t.Error(err)
			}
		case "/api/auth/session":
			if err := json.NewEncoder(writer).Encode(remote.Authorization{IsAuthenticated: true, Scopes: []string{"orchestration:read", "orchestration:operate"}, Auth: &remote.AuthDescriptor{Policy: "remote-reachable", BootstrapMethods: []string{"one-time-token"}, SessionMethods: []string{"bearer-access-token"}, SessionCookieName: "fixture-cookie"}}); err != nil {
				t.Error(err)
			}
		case "/api/auth/websocket-ticket":
			if err := json.NewEncoder(writer).Encode(map[string]string{"ticket": "fixture-ticket", "expiresAt": time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}); err != nil {
				t.Error(err)
			}
		case "/ws":
			socket, err := websocket.Accept(writer, request, nil)
			if err != nil {
				return
			}
			defer socket.CloseNow()
			for {
				_, data, err := socket.Read(request.Context())
				if err != nil {
					return
				}
				var frame struct {
					Tag    string `json:"_tag"`
					ID     string `json:"id"`
					Method string `json:"tag"`
				}
				if json.Unmarshal(data, &frame) != nil {
					return
				}
				if frame.Tag == "Ping" {
					if socket.Write(request.Context(), websocket.MessageText, []byte(`{"_tag":"Pong"}`)) != nil {
						return
					}
					continue
				}
				if frame.Tag != "Request" {
					continue
				}
				var value any = map[string]any{}
				switch frame.Method {
				case "server.getUsageSummary":
					summaryCalls.Add(1)
					value = remote.Summary{ContractVersion: 6, ReadAt: service.Now().Format(time.RFC3339), TimeZone: "UTC", SinceDay: "2026-09-07", UntilDay: "2026-10-06", Buckets: []accounting.Bucket{}, Sources: []remote.Source{}, Pricing: remote.Pricing{Status: "cached", Source: "fixture", KnownModels: 1}}
				case "server.refreshProviders":
					limitsCalls.Add(1)
				case "server.refreshUsageRates":
				default:
					continue
				}
				response, err := json.Marshal(map[string]any{"_tag": "Exit", "requestId": frame.ID, "exit": map[string]any{"_tag": "Success", "value": value}})
				if err != nil {
					t.Error(err)
					return
				}
				if err := socket.Write(request.Context(), websocket.MessageText, response); err != nil {
					return
				}
			}
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	connection := remote.Connection{EnvironmentId: "remote-fixture", Url: server.URL, CredentialId: "fixture"}
	service.Connector = remote.Connector{Secrets: memoryCredentials{"fixture": "synthetic-bearer"}}
	var finalInvalidation atomic.Bool
	service.Emit = func(name string, value any) {
		if name == "usage-invalidated" && limitsCalls.Load() == 1 {
			finalInvalidation.Store(true)
		}
	}
	connected := make(chan bool, 1)
	service.OnConnected = func() {
		service.mutex.Lock()
		state := service.connections[connection.EnvironmentId]
		isReady := state != nil && state.client != nil
		service.mutex.Unlock()
		connected <- isReady
	}
	saved, err := service.Configuration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	saved.Value.Connections = []ConnectionPreference{{Connection: connection, IsEnabled: true}}
	if _, err := service.Store.WriteSetting(ctx, "configuration", saved.Revision, saved.Value); err != nil {
		t.Fatal(err)
	}
	service.ReconcileConnections()
	defer service.Close()
	select {
	case ready := <-connected:
		if !ready {
			t.Fatal("hook ran before connection was installed")
		}
	case <-ctx.Done():
		t.Fatal("connection did not kick scheduler")
	}
	if _, err := service.Store.WriteSetting(ctx, "history-index", 0, "invalid history index"); err != nil {
		t.Fatal(err)
	}
	service.RateClient = &http.Client{Transport: fixtureTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"fixture":{"models":{"fixture":{"cost":{"input":1,"output":2}}}}}`))}, nil
	})}
	if err := service.RefreshCycle(ctx, "UTC", refresh.Request{IsManual: true}); err == nil {
		t.Fatal("history failure was hidden")
	}
	if summaryCalls.Load() != 4 || limitsCalls.Load() != 1 || !finalInvalidation.Load() {
		t.Fatal("partial failure blocked remaining windows, limits or invalidation", summaryCalls.Load(), limitsCalls.Load(), finalInvalidation.Load())
	}
	for _, days := range []int{1, 7, 30, 90} {
		window, err := service.viewWindow(days, "UTC", false)
		if err != nil {
			t.Fatal(err)
		}
		var summary remote.Summary
		if _, err := service.Store.ReadSetting(ctx, "remote-usage:"+connection.EnvironmentId+":"+windowKey(window), &summary); err != nil || summary.ContractVersion != 6 {
			t.Fatal("remote window was not cached", days, err)
		}
	}
}
