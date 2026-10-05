package quota

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
	"yousage/internal/managedprocess"
)

type credentialRequest struct {
	Environment map[string]string `json:"environment"`
	Home        string            `json:"home"`
}
type credentialResponse struct {
	Credential *grokCredential `json:"credential"`
	HasError   bool            `json:"hasError"`
}

// RunCredentialHelper isolates the default Grok credential filesystem read from the desktop process.
func RunCredentialHelper(input io.Reader, output io.Writer) int {
	var request credentialRequest
	response := credentialResponse{}
	if json.NewDecoder(io.LimitReader(input, 512<<10)).Decode(&request) != nil {
		response.HasError = true
	} else {
		credential, err := grokLogin(HttpOptions{Environment: request.Environment, Home: request.Home})
		response.Credential, response.HasError = credential, err != nil
	}
	if json.NewEncoder(output).Encode(response) != nil {
		return 1
	}
	return 0
}

func readGrokCredential(ctx context.Context, options HttpOptions) (*grokCredential, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if options.ReadFile != nil {
		credential, err := grokLogin(options)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return credential, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, errors.New("credential helper is unavailable")
	}
	input, err := json.Marshal(credentialRequest{Environment: options.Environment, Home: options.Home})
	if err != nil || len(input) > 512<<10 {
		return nil, errors.New("credential input is invalid")
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	data, err := managedprocess.Run(deadline, managedprocess.Options{Executable: executable, Arguments: []string{"--quota-credential-helper"}}, input, 64<<10)
	if err != nil {
		return nil, errors.New("credential read could not finish")
	}
	var response credentialResponse
	if json.Unmarshal(data, &response) != nil || response.HasError {
		return nil, errors.New("credential read failed")
	}
	return response.Credential, nil
}
