package sync

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

// TestEnvelopeVersion distinguishes unsupported formats from authentication failures.
func TestEnvelopeVersion(test *testing.T) {
	key, _ := NewKey()
	envelope, err := Seal([]byte(`{}`), key, "space:version")
	if err != nil {
		test.Fatal(err)
	}
	envelope[0] = 2
	if _, err = Open(envelope, key, "space:version"); !errors.Is(err, ErrEnvelopeVersion) {
		test.Fatalf("unsupported version error = %v", err)
	}
	for _, invalid := range [][]byte{nil, {1}, {1, 2, 3}} {
		if _, err = Open(invalid, key, "space:version"); err == nil || errors.Is(err, ErrEnvelopeVersion) {
			test.Fatalf("invalid envelope error = %v", err)
		}
	}
}

// TestEnvelopeBusinessFieldPreservation authenticates and compares every populated report field.
func TestEnvelopeBusinessFieldPreservation(test *testing.T) {
	expected := paritySnapshot(test)
	plaintext, err := json.Marshal(expected)
	if err != nil {
		test.Fatal(err)
	}
	key, err := NewKey()
	if err != nil {
		test.Fatal(err)
	}
	envelope, err := Seal(plaintext, key, "space:synthetic-parity")
	if err != nil {
		test.Fatal(err)
	}
	opened, err := Open(envelope, key, "space:synthetic-parity")
	if err != nil || !bytes.Equal(opened, plaintext) {
		test.Fatal("encryption/compression changed report JSON", err)
	}
	var actual Snapshot
	if err := json.Unmarshal(opened, &actual); err != nil || !reflect.DeepEqual(actual, expected) {
		test.Fatal("envelope lost business fields", err)
	}
}

// TestParityOracleDetectsLoss proves field stripping and wrong-period reads fail the same
// equality used above, using disposable in-memory copies rather than changing production.
func TestParityOracleDetectsLoss(test *testing.T) {
	expected := paritySnapshot(test)
	encoded, err := json.Marshal(expected)
	if err != nil {
		test.Fatal(err)
	}
	for _, field := range []string{"totals", "categoryCostUsd", "sources", "resetCredits", "history", "unavailable", "idleMs", "tokens", "decodeTokensPerSecond", "medianLatencyMs", "cacheHitRate", "unmeasuredRequests", "model", "display", "appearance", "bootstrap", "wrong period"} {
		test.Run(field, func(test *testing.T) {
			var altered map[string]any
			if err := json.Unmarshal(encoded, &altered); err != nil {
				test.Fatal(err)
			}
			if field == "wrong period" {
				periods := altered["periods"].(map[string]any)
				periods["7"] = periods["30"]
			} else {
				stripParityField(altered, field)
			}
			changed, err := json.Marshal(altered)
			if err != nil {
				test.Fatal(err)
			}
			var actual Snapshot
			if err := json.Unmarshal(changed, &actual); err != nil {
				test.Fatal("negative control must remain decodable", err)
			}
			if reflect.DeepEqual(actual, expected) {
				test.Fatal("preservation oracle missed injected loss", field)
			}
		})
	}
}

// stripParityField simulates a lossy projection of one field anywhere in a private copy.
func stripParityField(value any, field string) {
	switch value := value.(type) {
	case map[string]any:
		delete(value, field)
		for _, nested := range value {
			stripParityField(nested, field)
		}
	case []any:
		for _, nested := range value {
			stripParityField(nested, field)
		}
	}
}

func TestEnvelopeContractVector(test *testing.T) {
	data, err := os.ReadFile("testdata/envelope_vector.json")
	if err != nil {
		test.Fatal(err)
	}
	var vector struct{ KeyBase64Url, Aad, PlaintextJson, EnvelopeBase64 string }
	if err = json.Unmarshal(data, &vector); err != nil {
		test.Fatal(err)
	}
	envelope, err := base64.StdEncoding.DecodeString(vector.EnvelopeBase64)
	if err != nil {
		test.Fatal(err)
	}
	plaintext, err := Open(envelope, vector.KeyBase64Url, vector.Aad)
	if err != nil || string(plaintext) != vector.PlaintextJson {
		test.Fatalf("vector plaintext = %s, error = %v", plaintext, err)
	}
}

func TestEnvelopeRoundTripAndAuthentication(test *testing.T) {
	key, _ := NewKey()
	otherKey, _ := NewKey()
	plaintext := []byte(`{"message":"receipt","tokens":42}`)
	envelope, err := Seal(plaintext, key, "share:AAAAAAAAAAAAAAAAAAAAAA")
	if err != nil {
		test.Fatal(err)
	}
	second, _ := Seal(plaintext, key, "share:AAAAAAAAAAAAAAAAAAAAAA")
	if bytes.Equal(envelope, second) {
		test.Fatal("nonces were reused")
	}
	tampered := bytes.Clone(envelope)
	tampered[len(tampered)-1] ^= 1
	for _, scenario := range []struct {
		name       string
		data       []byte
		key, aad   string
		shouldFail bool
	}{
		{"round trip", envelope, key, "share:AAAAAAAAAAAAAAAAAAAAAA", false},
		{"wrong key", envelope, otherKey, "share:AAAAAAAAAAAAAAAAAAAAAA", true},
		{"wrong identity", envelope, key, "share:BBBBBBBBBBBBBBBBBBBBBB", true},
		{"wrong kind", envelope, key, "space:AAAAAAAAAAAAAAAAAAAAAA", true},
		{"tamper", tampered, key, "share:AAAAAAAAAAAAAAAAAAAAAA", true},
		{"truncated", envelope[:12], key, "share:AAAAAAAAAAAAAAAAAAAAAA", true},
		{"invalid key", envelope, "invalid", "share:AAAAAAAAAAAAAAAAAAAAAA", true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			opened, err := Open(scenario.data, scenario.key, scenario.aad)
			if (err != nil) != scenario.shouldFail {
				test.Fatalf("unexpected authentication result: %v", err)
			}
			if !scenario.shouldFail && !bytes.Equal(opened, plaintext) {
				test.Fatal("plaintext changed")
			}
		})
	}
}
