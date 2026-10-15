package sync

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

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
