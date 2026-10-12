package theme

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestPinnedGeneratorGoldens(test *testing.T) {
	data, err := os.ReadFile("../../tools/parity/fixtures/theme-golden.json")
	if err != nil {
		test.Fatal(err)
	}
	var cases []struct {
		Background string `json:"background"`
		Accent     string `json:"accent"`
		Appearance string `json:"appearance"`
		Colors     Colors `json:"colors"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		test.Fatal(err)
	}
	for _, fixture := range cases {
		actual, err := Generate(fixture.Appearance, fixture.Background, fixture.Accent)
		if err != nil || len(actual) != 61 {
			test.Fatalf("incomplete palette: %d %v", len(actual), err)
		}
		for role, expected := range fixture.Colors {
			if actual[role] != expected {
				test.Errorf("%s/%s %s: got %s, want %s", fixture.Background, fixture.Appearance, role, actual[role], expected)
			}
		}
	}
}

func TestColorAndContrastContracts(test *testing.T) {
	if math.Abs(contrast(rgb{255, 255, 255}, rgb{})-21) > 1e-12 {
		test.Fatal("white/black contrast is not21")
	}
	canonical, err := CanonicalColor("oklch(50% 20% -30 / none)")
	if err != nil || canonical != "oklch(0.5 0.08 330 / 0)" {
		test.Fatalf("CSS missing/percentage channels changed: %s %v", canonical, err)
	}
	first, _ := Generate("dark", "#172554", "#ff006680")
	second, _ := Generate("light", "#172554", "#ff0066")
	for role, value := range first {
		if second[role] != value {
			test.Fatalf("seed alpha/appearance slot changed actual canvas derivation: %s", role)
		}
	}
	if _, err := CanonicalColor("oklch(NaN 1 2)"); err == nil {
		test.Fatal("non-finite input accepted")
	}
}
