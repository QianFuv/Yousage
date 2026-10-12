package storage

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"yousage/internal/domain"
)

// TestLegacyPayloadAndExtendedFacts preserves older payloads alongside duration and context facts.
func TestLegacyPayloadAndExtendedFacts(t *testing.T) {
	values := map[int64]string{0: ""}
	intern := func(value string) (int64, error) {
		for id, existing := range values {
			if existing == value {
				return id, nil
			}
		}
		id := int64(len(values))
		values[id] = value
		return id, nil
	}
	lookup := func(id int64) (string, bool) { value, exists := values[id]; return value, exists }
	legacy := usageRecord("model")
	data, err := encodeRecord(legacy, intern)
	if err != nil {
		t.Fatal(err)
	}
	data = data[:len(data)-4]
	data[0] = 1
	decoded, err := decodeRecord(data, legacy.TimestampMs, "", false, nil, lookup)
	if err != nil || !reflect.DeepEqual(decoded, legacy) {
		t.Fatal("v1 facts changed", decoded, legacy, err)
	}
	data, err = encodeRecord(legacy, intern)
	if err != nil {
		t.Fatal(err)
	}
	data = data[:len(data)-2]
	data[0] = 2
	decoded, err = decodeRecord(data, legacy.TimestampMs, "", false, nil, lookup)
	if err != nil || !reflect.DeepEqual(decoded, legacy) {
		t.Fatal("v2 facts changed", decoded, legacy, err)
	}
	data, err = encodeRecord(legacy, intern)
	if err != nil {
		t.Fatal(err)
	}
	data = data[:len(data)-1]
	data[0] = 3
	decoded, err = decodeRecord(data, legacy.TimestampMs, "", false, nil, lookup)
	if err != nil || !reflect.DeepEqual(decoded, legacy) {
		t.Fatal("v3 facts changed", decoded, legacy, err)
	}
	for _, record := range []domain.Record{{Provider: domain.Claude, SessionId: "session", TimestampMs: 123, ErrorKind: "rateLimit"}, {Provider: domain.Claude, Model: "model", SessionId: "session", TimestampMs: 124, Speed: "fast", CacheTtlMs: 3600000, Totals: domain.Tokens{CacheCreationTokens: 50000}}} {
		data, err := encodeRecord(record, intern)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := decodeRecord(data, record.TimestampMs, "", false, nil, lookup)
		if err != nil || !reflect.DeepEqual(record, actual) {
			t.Fatal(record, actual, err)
		}
	}
}

// TestMixedPayloadUpgradeReplacementAndRetention keeps mixed versions readable and reclaimable.
func TestMixedPayloadUpgradeReplacementAndRetention(t *testing.T) {
	store, _, id := openFixture(t)
	ctx := context.Background()
	record := usageRecord("legacy")
	checkpoint := domain.Checkpoint{SourceId: id, Path: "synthetic", ModifiedNs: 10, Reducer: json.RawMessage(`{}`)}
	checkpoint, err := store.CommitFile(ctx, checkpoint, []domain.Record{record}, true)
	if err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := store.database.QueryRowContext(ctx, `SELECT data FROM records`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	payload = payload[:len(payload)-4]
	payload[0] = 1
	if _, err := store.database.ExecContext(ctx, `UPDATE records SET data=?`, payload); err != nil {
		t.Fatal(err)
	}
	failure := domain.Record{Provider: domain.Claude, TimestampMs: record.TimestampMs + 1, SessionId: "synthetic", ErrorKind: "server"}
	checkpoint, err = store.CommitFile(ctx, checkpoint, []domain.Record{failure}, false)
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.Records(ctx, id)
	if err != nil || len(records) != 2 || records[1].ErrorKind != "server" {
		t.Fatal(records, err)
	}
	checkpoint, err = store.CommitFile(ctx, checkpoint, []domain.Record{failure}, true)
	if err != nil {
		t.Fatal(err)
	}
	records, err = store.Records(ctx, id)
	if err != nil || len(records) != 1 || !reflect.DeepEqual(records[0], failure) {
		t.Fatal(records, err)
	}
	if err := store.PruneHistory(ctx, id, 11); err != nil {
		t.Fatal(err)
	}
	records, err = store.Records(ctx, id)
	if err != nil || len(records) != 0 {
		t.Fatal(records, err)
	}
	var count int
	if err := store.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM strings WHERE id<>0`).Scan(&count); err != nil || count != 0 {
		t.Fatal("dictionary references leaked", count, err)
	}
}

func TestErrorFactsRejectArbitraryData(t *testing.T) {
	failure := domain.Record{Provider: domain.Codex, TimestampMs: 123, SessionId: "session", ErrorKind: "server"}
	if failure.Validate() != nil {
		t.Fatal("valid error rejected")
	}
	for _, change := range []func(*domain.Record){func(record *domain.Record) { record.ErrorKind = "private arbitrary error" }, func(record *domain.Record) { record.Totals.OutputTokens = 1 }, func(record *domain.Record) { record.Speed = "standard" }, func(record *domain.Record) { cost := 1.0; record.ReportedCostUsd = &cost }, func(record *domain.Record) { record.Quota = &domain.QuotaObservation{} }} {
		record := failure
		change(&record)
		if record.Validate() == nil {
			t.Fatal("invalid error accepted", record)
		}
	}
}
