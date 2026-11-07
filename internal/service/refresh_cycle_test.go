package service

import (
	"context"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"yousage/internal/refresh"
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
