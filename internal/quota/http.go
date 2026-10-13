package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
)

type HttpOptions struct {
	Environment    map[string]string
	Home           string
	ReadFile       func(string) ([]byte, error)
	Client         *http.Client
	ClaudeEndpoint string
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
