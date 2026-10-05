package preferences

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"yousage/internal/domain"
	"yousage/internal/storage"
)

func TestSavedDisplayPricingAndSources(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "preferences.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := New(store)
	ctx := context.Background()
	display, err := service.Display(ctx)
	if err != nil || display.Revision != 0 || display.Value.Metric != "limits" || display.Value.WindowDays != 30 {
		t.Fatal(display, err)
	}
	old := display
	display.Value.Density, display.Value.Zoom = "compact", 2
	display, err = service.SaveDisplay(ctx, display)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveDisplay(ctx, old); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("stale UI overwrote saved settings", err)
	}
	recovered, err := service.Display(ctx)
	if err != nil || recovered.Value.Zoom != 2 || recovered.Value.Density != "compact" {
		t.Fatal(recovered, err)
	}
	pricing, err := service.Pricing(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	zero := 0.0
	pricing.Value.Overrides["Exact[1m]"] = domain.PriceOverride{CacheReadCostPerMillionTokens: &zero}
	pricing.Value.Aliases["source"] = "target"
	if _, err := service.SavePricing(ctx, "local", pricing); err != nil {
		t.Fatal(err)
	}
	recoveredPricing, err := service.Pricing(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	price := recoveredPricing.Value.Overrides["Exact[1m]"]
	if price.CacheReadCostPerMillionTokens == nil || *price.CacheReadCostPerMillionTokens != 0 || price.CacheWriteCostPerMillionTokens != nil {
		t.Fatal("blank vs zero lost")
	}
	target := "target"
	patched, err := service.PatchPricing(ctx, "local", recoveredPricing.Revision, PricingPatch{Aliases: map[string]*string{"Exact[1m]": &target}})
	if err != nil || len(patched.Value.Overrides) != 0 || patched.Value.Aliases["source"] != "target" {
		t.Fatal("alias patch failed to clear only source price", patched, err)
	}
	remote, err := service.Pricing(ctx, "remote")
	if err != nil || len(remote.Value.Overrides) != 0 {
		t.Fatal("environment preferences leaked", remote, err)
	}
	sources := []Source{{Id: "source", Provider: domain.Codex, Root: "/fixture", IsEnabled: false}}
	if _, err := service.SaveSources(ctx, sources, 0); err != nil {
		t.Fatal(err)
	}
	recoveredSources, _, err := service.Sources(ctx)
	if err != nil || recoveredSources[0].IsEnabled {
		t.Fatal("disabled history configuration lost", err)
	}
}
func TestInvalidPreferencesAndWindowRecovery(t *testing.T) {
	display := Defaults()
	display.Zoom = math.NaN()
	if display.Validate() == nil {
		t.Fatal("NaN zoom accepted")
	}
	display = Defaults()
	display.WindowDays = 2
	if display.Validate() == nil {
		t.Fatal("unknown period accepted")
	}
	area := WindowBounds{X: 0, Y: 0, Width: 1024, Height: 768}
	restored := RestoreBounds(WindowBounds{X: 3000, Y: 1000, Width: 1280, Height: 900, Maximized: true}, []WindowBounds{area})
	if restored.X != 0 || restored.Y != 0 || restored.Width != 1024 || restored.Height != 768 || !restored.Maximized {
		t.Fatal(restored)
	}
	negative := WindowBounds{X: -1920, Y: 0, Width: 1920, Height: 1080}
	restored = RestoreBounds(WindowBounds{X: -1800, Y: 20, Width: 800, Height: 700}, []WindowBounds{area, negative})
	if restored.X != -1800 || restored.Y != 20 {
		t.Fatal("valid secondary monitor moved", restored)
	}
}

func TestEmptySavedPricingCanBePatched(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "empty.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := New(store)
	ctx := context.Background()
	saved, err := service.SavePricing(ctx, "local", SavedPricing{})
	if err != nil {
		t.Fatal(err)
	}
	target := "target"
	price := domain.PriceOverride{InputCostPerMillionTokens: 1, OutputCostPerMillionTokens: 2}
	patched, err := service.PatchPricing(ctx, "local", saved.Revision, PricingPatch{Overrides: map[string]*domain.PriceOverride{"priced": &price}, Aliases: map[string]*string{"source": &target}})
	if err != nil || len(patched.Value.Overrides) != 1 || patched.Value.Aliases["source"] != "target" {
		t.Fatal(patched, err)
	}
}
