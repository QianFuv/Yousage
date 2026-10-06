// Package cursor collects account-wide billed usage through the authenticated dashboard.
package cursor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"yousage/internal/domain"
)

const dashboardUrl = "https://cursor.com/api/dashboard/get-filtered-usage-events"
const pageSize = 1000

type CredentialSource struct {
	Path               string
	IsKeychain         bool
	IsDisabled         bool
	NeedsKeychainOptIn bool
	Notice             string
}
type Options struct {
	Credentials   CredentialSource
	Client        *http.Client
	KeychainToken func(context.Context) (string, error)
	ReadFile      func(string) ([]byte, error)
}
type Result struct {
	AccountKey string          `json:"accountKey"`
	Records    []domain.Record `json:"records"`
	IsMissing  bool            `json:"missing"`
	Error      string          `json:"error,omitempty"`
	Notice     string          `json:"notice,omitempty"`
}

// DiscoverCredentials deliberately skips saved login when the CLI selected ephemeral/API authentication.
func DiscoverCredentials(platform, home string, environment map[string]string, isKeychainEnabled bool) CredentialSource {
	if strings.TrimSpace(environment["CURSOR_AUTH_TOKEN"]) != "" || strings.TrimSpace(environment["CURSOR_API_KEY"]) != "" || environment["AGENT_CLI_CREDENTIAL_STORE"] == "memory" {
		return CredentialSource{IsDisabled: true, Notice: "Cursor saved CLI login is unavailable with the current credential store."}
	}
	userHome := home
	key := "HOME"
	if platform == "windows" {
		key = "USERPROFILE"
	}
	if environment[key] != "" {
		userHome = environment[key]
	}
	if platform == "darwin" {
		if environment["AGENT_CLI_CREDENTIAL_STORE"] == "file" {
			return CredentialSource{Path: filepath.Join(userHome, ".cursor", "auth.json")}
		}
		if !isKeychainEnabled {
			return CredentialSource{IsDisabled: true, NeedsKeychainOptIn: true, Notice: "Enable Cursor Keychain usage on this environment to read account history."}
		}
		return CredentialSource{IsKeychain: true}
	}
	config := environment["XDG_CONFIG_HOME"]
	if !filepath.IsAbs(config) {
		config = filepath.Join(userHome, ".config")
	}
	folder := "cursor"
	if platform == "windows" {
		config = environment["APPDATA"]
		if config == "" {
			config = filepath.Join(userHome, "AppData", "Roaming")
		}
		folder = "Cursor"
	}
	return CredentialSource{Path: filepath.Join(config, folder, "auth.json")}
}

var rateSuffix = regexp.MustCompile(`(?:-thinking)?(?:-(?:none|minimal|low|medium|high|xhigh|max))?(?:-fast)?$`)

func RateModel(model string) string {
	model = rateSuffix.ReplaceAllString(strings.TrimPrefix(model, "cursor-"), "")
	if strings.HasPrefix(model, "grok-") {
		return "xai/" + model
	}
	return model
}
func hash(value []byte) string { digest := sha256.Sum256(value); return hex.EncodeToString(digest[:]) }
func object(value any) map[string]any {
	result, _ := value.(map[string]any)
	if result == nil {
		result = map[string]any{}
	}
	return result
}
func stringify(value any) []byte {
	var result bytes.Buffer
	encoder := json.NewEncoder(&result)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return bytes.TrimSuffix(result.Bytes(), []byte("\n"))
}
func boundaryOverlap(previous, current []string) int {
	sequence := append(append(append([]string{}, current...), ""), previous...)
	lengths := make([]int, len(sequence))
	for index := 1; index < len(sequence); index++ {
		length := lengths[index-1]
		for length > 0 && sequence[index] != sequence[length] {
			length = lengths[length-1]
		}
		if sequence[index] == sequence[length] {
			length++
		}
		lengths[index] = length
	}
	if len(lengths) == 0 {
		return 0
	}
	return lengths[len(lengths)-1]
}

// Read never returns partial pages; a failed refresh leaves the caller's last successful snapshot intact.
func Read(ctx context.Context, options Options, sinceMs, endMs int64) Result {
	result := Result{Records: []domain.Record{}}
	if options.Credentials.IsDisabled {
		result.IsMissing = true
		result.Notice = options.Credentials.Notice
		return result
	}
	readFile := options.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	var token string
	var err error
	if options.Credentials.IsKeychain {
		if options.KeychainToken == nil {
			err = errors.New("keychain unavailable")
		} else {
			token, err = options.KeychainToken(ctx)
		}
	} else {
		var data []byte
		data, err = readFile(options.Credentials.Path)
		if err == nil {
			var credential map[string]any
			err = json.Unmarshal(data, &credential)
			if err == nil {
				token, _ = credential["accessToken"].(string)
			}
		}
	}
	if err != nil {
		if !options.Credentials.IsKeychain && errors.Is(err, os.ErrNotExist) {
			result.IsMissing = true
			return result
		}
		result.Error = "Cursor credentials could not be read."
		if options.Credentials.IsKeychain {
			result.Error = "Cursor Keychain credentials could not be read."
			if errors.Is(err, context.DeadlineExceeded) {
				result.Error = "Allow Keychain access on the Mac running Yousage, then refresh."
			}
		}
		return result
	}
	if token == "" {
		result.IsMissing = true
		if options.Credentials.IsKeychain {
			result.Error = "Cursor account history needs a macOS Keychain CLI login on this environment."
		}
		return result
	}
	failure := func() Result {
		result.Records = []domain.Record{}
		result.Error = "Cursor account usage could not be read."
		return result
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return failure()
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return failure()
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return failure()
	}
	subject, _ := claims["sub"].(string)
	userParts := strings.Split(subject, "|")
	userId := userParts[len(userParts)-1]
	if subject == "" || userId == "" {
		return failure()
	}
	result.AccountKey = hash([]byte(subject))
	if sinceMs < 0 || sinceMs > endMs {
		return failure()
	}
	deadline, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	client := options.Client
	if client == nil {
		client = &http.Client{}
	}
	ownedClient := *client
	ownedClient.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("dashboard redirects are not allowed") }
	pages := [][]any{}
	var total *int64
	for page := 1; ; page++ {
		limit := int64(1000)
		if total != nil {
			limit = (*total+pageSize-1)/pageSize*2 + 1
		}
		if int64(page) > limit {
			return failure()
		}
		body := stringify(map[string]any{"page": page, "pageSize": pageSize, "startDate": strconv.FormatInt(sinceMs, 10), "endDate": strconv.FormatInt(endMs, 10)})
		pageContext, pageCancel := context.WithTimeout(deadline, 10*time.Second)
		request, err := http.NewRequestWithContext(pageContext, http.MethodPost, dashboardUrl, bytes.NewReader(body))
		if err != nil {
			pageCancel()
			return failure()
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://cursor.com")
		cookie := url.QueryEscape(userId + "::" + token)
		cookie = strings.ReplaceAll(cookie, "+", "%20")
		request.Header.Set("Cookie", "WorkosCursorSessionToken="+cookie)
		response, err := ownedClient.Do(request)
		if err != nil {
			pageCancel()
			return failure()
		}
		if response.StatusCode == 401 || response.StatusCode == 403 {
			response.Body.Close()
			pageCancel()
			result.Error = "Sign in to Cursor again to read account usage."
			return result
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			pageCancel()
			return failure()
		}
		var parsed any
		decoder := json.NewDecoder(response.Body)
		err = decoder.Decode(&parsed)
		if err == nil {
			var extra any
			if trailing := decoder.Decode(&extra); trailing != io.EOF {
				err = errors.New("invalid trailing JSON")
			}
		}
		response.Body.Close()
		pageCancel()
		bodyObject, ok := parsed.(map[string]any)
		if err != nil || !ok || bodyObject == nil {
			return failure()
		}
		for _, key := range []string{"error", "message", "code"} {
			if _, exists := bodyObject[key]; exists {
				return failure()
			}
		}
		count, hasCount := bodyObject["totalUsageEventsCount"]
		if len(bodyObject) == 0 {
			count, hasCount = float64(0), true
		}
		if hasCount {
			number, ok := count.(float64)
			if !ok || !domain.Finite(number) || number < 0 || number > float64(1<<53-1) || math.Trunc(number) != number {
				return failure()
			}
			integer := int64(number)
			if total != nil && *total != integer {
				return failure()
			}
			total = &integer
		}
		events, isArray := bodyObject["usageEventsDisplay"].([]any)
		if len(bodyObject) == 0 || (len(bodyObject) == 1 && hasCount) {
			events, isArray = []any{}, true
		}
		if !isArray || len(events) > pageSize {
			return failure()
		}
		pages = append(pages, events)
		if len(events) < pageSize {
			break
		}
	}
	rawCount := int64(0)
	for _, page := range pages {
		rawCount += int64(len(page))
	}
	if total != nil && rawCount < *total {
		return failure()
	}
	remaining := int64(0)
	if total != nil {
		remaining = rawCount - *total
	}
	previousKeys := []string{}
	occurrences := map[string]int{}
	for _, page := range pages {
		keys := []string{}
		if remaining > 0 {
			for _, event := range page {
				keys = append(keys, hash(stringify(event)))
			}
		}
		removals := min(remaining, int64(boundaryOverlap(previousKeys, keys)))
		remaining -= removals
		previousKeys = keys
		for _, raw := range page[int(removals):] {
			event := object(raw)
			if event["tokenUsage"] == nil {
				continue
			}
			usage := object(event["tokenUsage"])
			for _, key := range []string{"inputTokens", "outputTokens", "cacheReadTokens", "cacheWriteTokens", "totalCents"} {
				if value, exists := usage[key]; exists {
					number, ok := value.(float64)
					if !ok || !domain.Finite(number) || number < 0 {
						return failure()
					}
				}
			}
			instant, isNumber := event["timestamp"].(float64)
			if value, ok := event["timestamp"].(string); ok && strings.TrimSpace(value) != "" {
				instant, err = strconv.ParseFloat(strings.TrimSpace(value), 64)
				isNumber = err == nil
			}
			model, _ := event["model"].(string)
			if !isNumber || !domain.Finite(instant) || instant >= float64(math.MaxInt64) || instant <= float64(math.MinInt64) || model == "" {
				return failure()
			}
			if instant < float64(sinceMs) || instant > float64(endMs) {
				continue
			}
			tokens := func(key string) int64 {
				number, _ := usage[key].(float64)
				if number >= float64(math.MaxInt64) {
					return 0
				}
				return int64(number)
			}
			totals := domain.Tokens{UncachedInputTokens: tokens("inputTokens"), CachedInputTokens: tokens("cacheReadTokens"), CacheCreationTokens: tokens("cacheWriteTokens"), OutputTokens: tokens("outputTokens")}
			var cost *float64
			if cents, ok := usage["totalCents"].(float64); ok {
				amount := cents / 100
				cost = &amount
			}
			session, _ := event["conversationId"].(string)
			key := hash(stringify([]any{instant, model, session, totals, cost}))
			occurrence := occurrences[key]
			occurrences[key]++
			result.Records = append(result.Records, domain.Record{Provider: domain.Cursor, TimestampMs: int64(instant), Model: model, RateModel: RateModel(model), SessionId: session, Totals: totals, ReportedCostUsd: cost, Speed: "standard", DedupeKey: fmt.Sprintf("cursor-account:%s:%s:%d", result.AccountKey, key, occurrence)})
		}
		if deadline.Err() != nil {
			return failure()
		}
	}
	if remaining != 0 {
		return failure()
	}
	return result
}

// SourceIdentity deduplicates the same account across local and connected environments.
func (result Result) SourceIdentity() domain.SourceFingerprint {
	return domain.SourceFingerprint{HostId: "cursor.com", Provider: domain.Cursor, ResolvedHomePath: "cursor-account:" + result.AccountKey, VolumeId: result.AccountKey}
}
