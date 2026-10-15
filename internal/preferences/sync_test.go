package preferences_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"yousage/internal/preferences"
	"yousage/internal/storage"
	"yousage/internal/sync"
)

func TestSyncRecordIsValidatedAndVersioned(test *testing.T) {
	store, err := storage.Open(filepath.Join(test.TempDir(), "sync.sqlite"))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	service := preferences.New(store)
	settings := sync.Settings{TimeZone: "UTC", Shares: []sync.ShareRecord{}}
	ctx := context.Background()
	revision, err := service.Sync(ctx, &settings)
	if err != nil || revision != 0 {
		test.Fatal(revision, err)
	}
	settings.RelayUrl = "https://relay.example"
	revision, err = service.SaveSync(ctx, settings, revision)
	if err != nil || revision != 1 {
		test.Fatal(revision, err)
	}
	if _, err = service.SaveSync(ctx, settings, 0); !errors.Is(err, storage.ErrConflict) {
		test.Fatal("stale settings overwrote sync record", err)
	}
	for _, invalid := range []sync.Settings{
		{IsEnabled: true, TimeZone: "UTC"},
		{TimeZone: "invalid"},
		{TimeZone: "UTC", RelayUrl: "https://relay.example/path"},
		{TimeZone: "UTC", SpaceId: "bad"},
		{TimeZone: "UTC", Shares: []sync.ShareRecord{{Id: "AAAAAAAAAAAAAAAAAAAAAA", Days: 14, TtlDays: 90}}},
	} {
		if _, err = service.SaveSync(ctx, invalid, revision); err == nil {
			test.Fatal("invalid sync settings saved", invalid)
		}
	}
	loaded := sync.Settings{}
	if loadedRevision, err := service.Sync(ctx, &loaded); err != nil || loadedRevision != revision || loaded.RelayUrl != settings.RelayUrl {
		test.Fatal("sync record did not round trip", loaded, err)
	}
}
