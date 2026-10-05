package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"time"
	"yousage/internal/managedprocess"

	"github.com/zalando/go-keyring"
)

type helperRequest struct {
	Operation string `json:"operation"`
	Id        string `json:"id,omitempty"`
	Value     string `json:"value,omitempty"`
}
type helperResponse struct {
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}
type OwnedStore struct {
	executable string
	native     NativeStore
}

const helperFrameLimit = 16384

// NewOwnedStore isolates platform keyring subprocesses in a cancellable application helper.
func NewOwnedStore() (*OwnedStore, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, errors.New("application helper is unavailable")
	}
	return &OwnedStore{executable: executable}, nil
}
func (store *OwnedStore) helper(ctx context.Context, request helperRequest) (string, error) {
	input, err := json.Marshal(request)
	if err != nil {
		return "", errors.New("credential request is invalid")
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output, err := managedprocess.Run(deadline, managedprocess.Options{Executable: store.executable, Arguments: []string{"--credential-helper"}}, input, helperFrameLimit)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("native credential helper could not finish")
	}
	var response helperResponse
	if json.Unmarshal(output, &response) != nil {
		return "", errors.New("native credential helper returned an invalid response")
	}
	if response.Error == "missing" {
		return "", ErrMissing
	}
	if response.Error != "" {
		return "", errors.New("system credential store is unavailable")
	}
	return response.Value, nil
}
func (store *OwnedStore) Get(ctx context.Context, id string) (string, error) {
	if !validId(id) {
		return "", errors.New("invalid credential identifier")
	}
	if runtime.GOOS == "windows" {
		return store.native.Get(ctx, id)
	}
	return store.helper(ctx, helperRequest{Operation: "get", Id: id})
}
func (store *OwnedStore) Put(ctx context.Context, id, value string) error {
	if !validId(id) || len(value) == 0 || len(value) > 2048 {
		return errors.New("invalid credential identifier or size")
	}
	if runtime.GOOS == "windows" {
		return store.native.Put(ctx, id, value)
	}
	_, err := store.helper(ctx, helperRequest{Operation: "put", Id: id, Value: value})
	return err
}
func (store *OwnedStore) Delete(ctx context.Context, id string) error {
	if !validId(id) {
		return errors.New("invalid credential identifier")
	}
	if runtime.GOOS == "windows" {
		return store.native.Delete(ctx, id)
	}
	_, err := store.helper(ctx, helperRequest{Operation: "delete", Id: id})
	return err
}
func (store *OwnedStore) CursorToken(ctx context.Context) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("Cursor Keychain credentials require macOS")
	}
	value, err := store.helper(ctx, helperRequest{Operation: "cursor"})
	if errors.Is(err, ErrMissing) {
		return "", nil
	}
	return value, err
}

// RunHelper dispatches only application-owned credentials or the explicitly opted-in Cursor keychain entry.
func RunHelper(input io.Reader, output io.Writer) int {
	var request helperRequest
	response := helperResponse{}
	if json.NewDecoder(io.LimitReader(input, helperFrameLimit)).Decode(&request) != nil {
		response.Error = "invalid"
	} else {
		var value string
		var err error
		store := NativeStore{}
		switch request.Operation {
		case "get":
			value, err = store.Get(context.Background(), request.Id)
		case "put":
			err = store.Put(context.Background(), request.Id, request.Value)
		case "delete":
			err = store.Delete(context.Background(), request.Id)
		case "cursor":
			if runtime.GOOS != "darwin" {
				err = errors.New("unsupported")
			} else {
				value, err = keyring.Get("cursor-access-token", "cursor-user")
				if errors.Is(err, keyring.ErrNotFound) {
					err = ErrMissing
				}
			}
		default:
			err = errors.New("unsupported")
		}
		if errors.Is(err, ErrMissing) {
			response.Error = "missing"
		} else if err != nil {
			response.Error = "unavailable"
		} else {
			response.Value = value
		}
	}
	if json.NewEncoder(output).Encode(response) != nil {
		return 1
	}
	return 0
}
