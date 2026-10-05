package cursor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (transport transportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}
func fixtureToken(subject string) string {
	return "header." + base64.RawURLEncoding.EncodeToString(stringify(map[string]any{"sub": subject})) + ".signature"
}
func fixtureOptions(pages []any) (Options, *int) {
	calls := new(int)
	options := Options{Credentials: CredentialSource{Path: "owned-auth"}, ReadFile: func(string) ([]byte, error) {
		return stringify(map[string]any{"accessToken": fixtureToken("provider|account")}), nil
	}}
	options.Client = &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		*calls++
		if *calls > len(pages) {
			return nil, fmt.Errorf("unexpected extra page")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(stringify(pages[*calls-1])))), Header: make(http.Header), Request: request}, nil
	})}
	return options, calls
}
func event(index int) any {
	return map[string]any{"timestamp": float64(index + 1000), "model": "cursor-grok-4.6-high-fast", "conversationId": "session", "chargedCents": 999, "tokenUsage": map[string]any{"inputTokens": 10.9, "cacheReadTokens": 30, "cacheWriteTokens": 2, "outputTokens": 5, "totalCents": 25}}
}
func page(events []any, total int) any {
	return map[string]any{"usageEventsDisplay": events, "totalUsageEventsCount": total}
}
func sequence(first, count int) []any {
	result := []any{}
	for index := first; index < first+count; index++ {
		result = append(result, event(index))
	}
	return result
}

func TestAuthSelectionSkipsEphemeralAndUnapprovedKeychain(t *testing.T) {
	for _, environment := range []map[string]string{{"CURSOR_AUTH_TOKEN": " token "}, {"CURSOR_API_KEY": "key"}, {"AGENT_CLI_CREDENTIAL_STORE": "memory"}} {
		source := DiscoverCredentials("windows", t.TempDir(), environment, true)
		result := Read(context.Background(), Options{Credentials: source, ReadFile: func(string) ([]byte, error) { t.Fatal("read disabled credential"); return nil, nil }}, 0, 10000)
		if !source.IsDisabled || !result.IsMissing || result.Notice == "" {
			t.Fatal(source, result)
		}
	}
	home := t.TempDir()
	source := DiscoverCredentials("darwin", home, nil, false)
	if !source.IsDisabled {
		t.Fatal(source)
	}
	source = DiscoverCredentials("darwin", home, map[string]string{"AGENT_CLI_CREDENTIAL_STORE": "file"}, false)
	if source.Path != filepath.Join(home, ".cursor", "auth.json") {
		t.Fatal(source)
	}
	source = DiscoverCredentials("windows", home, map[string]string{"APPDATA": filepath.Join(home, "roaming")}, false)
	if source.Path != filepath.Join(home, "roaming", "Cursor", "auth.json") {
		t.Fatal(source)
	}
}

func TestAuthenticationErrorsAreRedacted(t *testing.T) {
	for _, fixture := range []struct {
		data    []byte
		err     error
		missing bool
		message string
	}{
		{nil, os.ErrNotExist, true, ""}, {[]byte("bad secret"), nil, false, "Cursor credentials could not be read."}, {[]byte(`{"accessToken":""}`), nil, true, ""}, {[]byte(`{"accessToken":"secret.invalid.signature"}`), nil, false, "Cursor account usage could not be read."},
	} {
		options := Options{Credentials: CredentialSource{Path: "owned"}, ReadFile: func(string) ([]byte, error) { return fixture.data, fixture.err }}
		result := Read(context.Background(), options, 0, 1000)
		if result.IsMissing != fixture.missing || result.Error != fixture.message || len(result.Records) != 0 {
			t.Fatal(result)
		}
	}
	options, _ := fixtureOptions(nil)
	options.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("sensitive server error"))}, nil
	})
	result := Read(context.Background(), options, 0, 1000)
	if result.AccountKey == "" || result.Error != "Sign in to Cursor again to read account usage." {
		t.Fatal(result)
	}
}

func TestRequestNormalizationAndIdenticalBilledOccurrences(t *testing.T) {
	options, calls := fixtureOptions([]any{page([]any{event(0), event(0), map[string]any{"tokenUsage": nil}}, 3)})
	underlying := options.Client.Transport
	options.Client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != dashboardUrl || request.Method != "POST" || request.Header.Get("Origin") != "https://cursor.com" || !strings.Contains(request.Header.Get("Cookie"), "account%3A%3Aheader.") {
			t.Fatal("request contract", request)
		}
		var body map[string]any
		if json.NewDecoder(request.Body).Decode(&body) != nil || body["pageSize"] != float64(1000) || body["startDate"] != "1000" || body["endDate"] != "1000" {
			t.Fatal(body)
		}
		return underlying.RoundTrip(request)
	})
	result := Read(context.Background(), options, 1000, 1000)
	if result.Error != "" || *calls != 1 || len(result.Records) != 2 {
		t.Fatal(result, *calls)
	}
	first := result.Records[0]
	if first.Totals.Total() != 47 || first.ReportedCostUsd == nil || *first.ReportedCostUsd != 0.25 || first.RateModel != "xai/grok-4.6" || !strings.HasSuffix(first.DedupeKey, ":0") || !strings.HasSuffix(result.Records[1].DedupeKey, ":1") {
		t.Fatal(result)
	}
	if !strings.Contains(first.DedupeKey, ":e73bde233ca18c9b12ffeceacf242b6f93444b8e411a5e87b9c77ee4d0a30115:") {
		t.Fatal("pinned JavaScript tuple hash differs", first.DedupeKey)
	}
	if result.SourceIdentity().HostId != "cursor.com" || result.SourceIdentity().VolumeId != result.AccountKey {
		t.Fatal(result.SourceIdentity())
	}
	zero := event(0).(map[string]any)
	zero["tokenUsage"] = map[string]any{"totalCents": 0}
	options, _ = fixtureOptions([]any{page([]any{zero}, 1)})
	result = Read(context.Background(), options, 0, 1000)
	if len(result.Records) != 1 || result.Records[0].ReportedCostUsd == nil || *result.Records[0].ReportedCostUsd != 0 {
		t.Fatal(result)
	}
}

func TestTerminalPageBoundaryOverlapAndBilledCopy(t *testing.T) {
	for _, total := range []int{2000, 2001} {
		options, calls := fixtureOptions([]any{page(sequence(0, 1000), total), page(sequence(999, 1000), total), page(sequence(1999, 1), total)})
		result := Read(context.Background(), options, 0, 10000)
		if result.Error != "" || *calls != 3 || len(result.Records) != total {
			t.Fatal(result.Error, *calls, len(result.Records))
		}
	}
	options, calls := fixtureOptions([]any{page(sequence(0, 1000), 1000), page([]any{}, 1000)})
	result := Read(context.Background(), options, 0, 10000)
	if result.Error != "" || *calls != 2 || len(result.Records) != 1000 {
		t.Fatal(result.Error, *calls)
	}
}

func TestInvalidPagesReturnNoPartialFacts(t *testing.T) {
	invalid := event(0).(map[string]any)
	invalid["tokenUsage"] = map[string]any{"inputTokens": -1}
	for _, pages := range [][]any{
		{page(sequence(0, 1000), 1001), page([]any{}, 1001)},
		{page(sequence(0, 1000), 1000), page([]any{}, 1001)},
		{page(sequence(0, 1000), 1000), page([]any{event(500)}, 1000)},
		{page([]any{event(0), invalid}, 2)},
		{map[string]any{"error": "secret details"}},
		{page(sequence(0, 1001), 1001)},
	} {
		options, _ := fixtureOptions(pages)
		result := Read(context.Background(), options, 0, 10000)
		if result.Error != "Cursor account usage could not be read." || len(result.Records) != 0 {
			t.Fatal(result.Error, len(result.Records))
		}
	}
	for _, empty := range []any{map[string]any{}, map[string]any{"totalUsageEventsCount": 0}, page([]any{}, 0)} {
		options, _ := fixtureOptions([]any{empty})
		result := Read(context.Background(), options, 0, 1000)
		if result.Error != "" || result.IsMissing || len(result.Records) != 0 {
			t.Fatal(result)
		}
	}
}

func TestLargeAccountAndRedirectRefusal(t *testing.T) {
	pages := []any{}
	for pageIndex := 0; pageIndex < 101; pageIndex++ {
		pages = append(pages, page(sequence(pageIndex*1000, 1000), 101000))
	}
	pages = append(pages, page([]any{}, 101000))
	options, calls := fixtureOptions(pages)
	result := Read(context.Background(), options, 0, 200000)
	if result.Error != "" || *calls != 102 || len(result.Records) != 101000 {
		t.Fatal(result.Error, *calls, len(result.Records))
	}
	options, _ = fixtureOptions(nil)
	redirects := 0
	options.Client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		redirects++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://another.example/credentials"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
	})
	result = Read(context.Background(), options, 0, 1000)
	if result.Error == "" || redirects != 1 {
		t.Fatal(result, redirects)
	}
}
