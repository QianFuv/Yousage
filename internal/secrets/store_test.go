package secrets

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNativeOwnedCredentialRoundTripAndRemoval(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native qualification is Windows first; other OS stores require native runners")
	}
	store := NativeStore{}
	id := "fixture-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	ctx := context.Background()
	t.Cleanup(func() {
		if err := store.Delete(ctx, id); err != nil {
			t.Error(err)
		}
	})
	if err := store.Put(ctx, id, "controlled-fixture-secret"); err != nil {
		t.Fatal(err)
	}
	value, err := store.Get(ctx, id)
	if err != nil || value != "controlled-fixture-secret" {
		t.Fatal("native roundtrip failed")
	}
	if err = store.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Get(ctx, id); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
}
func TestRejectUnsafeIdentifiersOversizeAndCancelledContext(t *testing.T) {
	store := NativeStore{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Get(ctx, "fixture"); err != context.Canceled {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "../outside", "fixture"); err == nil {
		t.Fatal("unsafe id")
	}
	if err := store.Put(context.Background(), "fixture", strings.Repeat("x", 2049)); err == nil {
		t.Fatal("oversized secret")
	}
}
