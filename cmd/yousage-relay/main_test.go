package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestInvalidTokenReturnsClearErrorWithoutLoggingToken keeps validation errors free of secrets.
func TestInvalidTokenReturnsClearErrorWithoutLoggingToken(t *testing.T) {
	for _, token := range []string{"", "short-secret"} {
		t.Run(token, func(t *testing.T) {
			var output bytes.Buffer
			err := run(context.Background(), func(name string) string {
				if name == "YOUSAGE_RELAY_TOKEN" {
					return token
				}
				return ""
			}, &output)
			if err == nil || !strings.Contains(err.Error(), "YOUSAGE_RELAY_TOKEN must contain at least 24 characters") {
				t.Fatalf("missing clear token validation error: %v", err)
			}
			if token != "" && strings.Contains(output.String()+err.Error(), token) {
				t.Fatal("token leaked in error or log")
			}
		})
	}
}

// TestStartupLogsAddressWithoutToken checks that startup logs contain only the listening address.
func TestStartupLogsAddressWithoutToken(t *testing.T) {
	const token = "sensitive-owner-token-never-log-this-value"
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	err := run(ctx, func(name string) string {
		switch name {
		case "YOUSAGE_RELAY_TOKEN":
			return token
		case "YOUSAGE_RELAY_ADDR":
			return "127.0.0.1:0"
		case "YOUSAGE_RELAY_DATA":
			return directory
		default:
			return ""
		}
	}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "relay listening on 127.0.0.1:") {
		t.Fatalf("startup log missing address: %s", &output)
	}
	if strings.Contains(output.String(), token) || strings.Contains(output.String(), "sensitive-owner") {
		t.Fatal("token leaked in startup log")
	}
}
