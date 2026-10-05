package quota

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type HttpOptions struct {
	Environment       map[string]string
	Home              string
	Platform          string
	ReadFile          func(string) ([]byte, error)
	Client            *http.Client
	KeychainToken     func(context.Context) (string, error)
	IsKeychainAllowed bool
	CursorEndpoint    string
	OpenCodeServerUrl string
	IsEnabled         bool
}

func (options HttpOptions) file(path string) ([]byte, error) {
	if options.ReadFile != nil {
		return options.ReadFile(path)
	}
	return os.ReadFile(path)
}
func (options HttpOptions) userHome() string {
	if home := options.Environment["HOME"]; home != "" {
		return home
	}
	if home := options.Environment["USERPROFILE"]; home != "" {
		return home
	}
	return options.Home
}
func (options HttpOptions) credentialFile(path, override string) ([]byte, error) {
	if override != "" {
		return []byte(override), nil
	}
	data, err := options.file(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte("{}"), nil
	}
	return data, err
}
func (options HttpOptions) request(ctx context.Context, method, endpoint, token string, headers map[string]string) (json.RawMessage, int, error) {
	var input io.Reader
	if method == http.MethodPost {
		input = bytes.NewReader([]byte("{}"))
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, input)
	if err != nil {
		return nil, 0, errors.New("invalid quota endpoint")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	client := http.Client{}
	if options.Client != nil {
		client = *options.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("quota endpoint redirects are not allowed")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.StatusCode, errors.New("quota request failed")
	}
	var data json.RawMessage
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if decoder.Decode(&data) != nil {
		return nil, response.StatusCode, errors.New("invalid quota JSON")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, response.StatusCode, errors.New("invalid quota trailing JSON")
	}
	return data, response.StatusCode, nil
}

// ReadCursor uses explicit CLI tokens before saved login; history's auth selection remains independent.
func ReadCursor(ctx context.Context, options HttpOptions) Limits {
	checkedAt := checked(time.Now())
	unsupported := unavailable(checkedAt, "unsupported", "")
	if !options.IsEnabled {
		return unsupported
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	failure := func() Limits { return unavailable(checkedAt, "probeFailed", "Cursor could not read usage limits.") }
	environment := options.Environment
	endpoint := strings.TrimSpace(options.CursorEndpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(environment["CURSOR_API_ENDPOINT"])
	}
	if endpoint == "" {
		endpoint = "https://api2.cursor.sh"
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	token := strings.TrimSpace(environment["CURSOR_AUTH_TOKEN"])
	if token == "" {
		if strings.TrimSpace(environment["CURSOR_API_KEY"]) != "" || environment["AGENT_CLI_CREDENTIAL_STORE"] == "memory" {
			return unsupported
		}
		if options.Platform == "darwin" && environment["AGENT_CLI_CREDENTIAL_STORE"] != "file" {
			if !options.IsKeychainAllowed {
				return unavailable(checkedAt, "unsupported", "Enable Cursor account usage to read its Keychain login.")
			}
			if endpoint != "https://api2.cursor.sh" {
				return unavailable(checkedAt, "unsupported", "Cursor Keychain usage requires the default Cursor endpoint.")
			}
			if options.KeychainToken == nil {
				return failure()
			}
			value, err := options.KeychainToken(deadline)
			if err != nil {
				return failure()
			}
			token = strings.TrimSpace(value)
		} else {
			home := options.userHome()
			directory := filepath.Join(home, ".config", "cursor")
			if config := environment["XDG_CONFIG_HOME"]; config != "" {
				directory = filepath.Join(config, "cursor")
			}
			if options.Platform == "darwin" {
				directory = filepath.Join(home, ".cursor")
			}
			if options.Platform == "windows" {
				home = environment["USERPROFILE"]
				if home == "" {
					home = options.Home
				}
				config := environment["APPDATA"]
				if config == "" {
					config = filepath.Join(home, "AppData", "Roaming")
				}
				directory = filepath.Join(config, "Cursor")
			}
			data, err := options.credentialFile(filepath.Join(directory, "auth.json"), "")
			if err != nil {
				return failure()
			}
			var credentials struct {
				Token *string `json:"accessToken"`
			}
			if !validObject(data, map[string]string{"accessToken": "string"}) || json.Unmarshal(data, &credentials) != nil {
				return failure()
			}
			if credentials.Token != nil {
				token = strings.TrimSpace(*credentials.Token)
			}
		}
	}
	if token == "" {
		return unsupported
	}
	data, _, err := options.request(deadline, http.MethodPost, endpoint+"/aiserver.v1.DashboardService/GetCurrentPeriodUsage", token, map[string]string{"connect-protocol-version": "1", "x-cursor-client-type": "cli"})
	if err != nil {
		return failure()
	}
	limits, err := MapCursor(data, checkedAt)
	if err != nil {
		return failure()
	}
	return limits
}

// ReadOpenCode excludes external servers and respects stored API authentication precedence.
func ReadOpenCode(ctx context.Context, options HttpOptions) Limits {
	checkedAt := checked(time.Now())
	unsupported := unavailable(checkedAt, "unsupported", "")
	if !options.IsEnabled || strings.TrimSpace(options.OpenCodeServerUrl) != "" {
		return unsupported
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	failure := func() Limits { return unavailable(checkedAt, "probeFailed", "OpenCode Go could not read usage.") }
	environment := options.Environment
	dataHome := environment["XDG_DATA_HOME"]
	if dataHome == "" {
		dataHome = filepath.Join(options.userHome(), ".local", "share")
	}
	data, err := options.credentialFile(filepath.Join(dataHome, "opencode", "auth.json"), environment["OPENCODE_AUTH_CONTENT"])
	if err != nil {
		return failure()
	}
	var credentials map[string]any
	if json.Unmarshal(data, &credentials) != nil || credentials == nil {
		return failure()
	}
	token := environment["OPENCODE_API_KEY"]
	if credential, ok := credentials["opencode-go"].(map[string]any); ok && credential["type"] == "api" {
		if key, ok := credential["key"].(string); ok {
			token = key
		}
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return unsupported
	}
	data, status, err := options.request(deadline, http.MethodGet, "https://opencode.ai/zen/go/v1/usage", token, nil)
	if status == 403 {
		return unsupported
	}
	if err != nil {
		return failure()
	}
	limits, err := MapOpenCode(data, checkedAt)
	if err != nil {
		return failure()
	}
	digest := sha256.Sum256([]byte("opencode-go\x00" + token))
	limits.CredentialFingerprint = hex.EncodeToString(digest[:])
	return limits
}

var grokConfig = regexp.MustCompile(`(?m)^\s*(?:\[\[?\s*)?["']?(?:auth|grok_com_config|endpoints)["']?\s*[.\]=]`)

type grokCredential struct {
	Key   string `json:"key"`
	Mode  string `json:"auth_mode"`
	Email string `json:"email"`
}

func grokLogin(options HttpOptions) (*grokCredential, error) {
	environment := options.Environment
	for _, key := range []string{"XAI_API_KEY", "GROK_OIDC_ISSUER", "GROK_OIDC_CLIENT_ID", "GROK_OAUTH2_ISSUER", "GROK_OAUTH2_CLIENT_ID", "GROK_OAUTH2_PRINCIPAL_TYPE", "GROK_OAUTH2_PRINCIPAL_ID", "GROK_AUTH_PROVIDER_COMMAND", "GROK_LOCAL_AUTH", "GROK_CLI_CHAT_PROXY_BASE_URL", "GROK_MODELS_BASE_URL", "GROK_CONFIG", "GROK_CONFIG_PATH"} {
		if strings.TrimSpace(environment[key]) != "" {
			return nil, nil
		}
	}
	home := strings.TrimSpace(environment["GROK_HOME"])
	if home == "" {
		home = filepath.Join(options.userHome(), ".grok")
	}
	for _, path := range []string{filepath.Join(home, "config.toml"), filepath.Join(home, "managed_config.toml"), filepath.Join(home, "requirements.toml"), "/etc/grok/managed_config.toml", "/etc/grok/requirements.toml"} {
		data, err := options.file(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if grokConfig.Match(data) {
			return nil, nil
		}
	}
	data, err := options.credentialFile(filepath.Join(home, "auth.json"), strings.TrimSpace(environment["GROK_AUTH"]))
	if err != nil {
		return nil, err
	}
	var credentials map[string]grokCredential
	var entries map[string]json.RawMessage
	if json.Unmarshal(data, &entries) != nil || entries == nil {
		return nil, errors.New("invalid Grok credentials")
	}
	for _, entry := range entries {
		if !validObject(entry, map[string]string{"key": "string", "auth_mode": "string", "email": "string"}) {
			return nil, errors.New("invalid Grok credentials")
		}
	}
	if json.Unmarshal(data, &credentials) != nil || credentials == nil {
		return nil, errors.New("invalid Grok credentials")
	}
	credential, ok := credentials["https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828"]
	if !ok {
		credential, ok = credentials["https://accounts.x.ai/sign-in"]
	}
	if !ok || credential.Mode == "api_key" {
		return nil, nil
	}
	return &credential, nil
}

// ReadGrok binds the returned email to the credential selected for this exact billing read.
func ReadGrok(ctx context.Context, options HttpOptions) Account {
	checkedAt := checked(time.Now())
	account := Account{Limits: unavailable(checkedAt, "unsupported", "")}
	if !options.IsEnabled {
		return account
	}
	failure := unavailable(checkedAt, "probeFailed", "Grok could not read usage limits.")
	credential, err := readGrokCredential(ctx, options)
	if err != nil || ctx.Err() != nil {
		account.Limits = failure
		return account
	}
	if credential == nil {
		return account
	}
	account.Email = strings.TrimSpace(credential.Email)
	token := strings.TrimSpace(credential.Key)
	if token == "" {
		return account
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	data, _, err := options.request(deadline, http.MethodGet, "https://cli-chat-proxy.grok.com/v1/billing?format=credits", token, nil)
	if err != nil {
		account.Limits = failure
		return account
	}
	limits, err := MapGrok(data, checkedAt)
	if err != nil {
		account.Limits = failure
		return account
	}
	account.Limits = limits
	return account
}
