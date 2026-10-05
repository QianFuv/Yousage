package secrets

import (
	"context"
	"errors"
	"regexp"

	"github.com/zalando/go-keyring"
)

var ErrMissing = errors.New("credential is not saved")
var credentialId = regexp.MustCompile(`^[a-zA-Z0-9-]{1,96}$`)

type Store interface {
	Get(context.Context, string) (string, error)
	Put(context.Context, string, string) error
	Delete(context.Context, string) error
}
type NativeStore struct{}

func validId(id string) bool { return credentialId.MatchString(id) }

// Get returns a native credential without including platform output in application errors.
func (NativeStore) Get(ctx context.Context, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !validId(id) {
		return "", errors.New("invalid credential identifier")
	}
	value, err := keyring.Get("Yousage", id)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrMissing
	}
	if err != nil {
		return "", errors.New("system credential store is unavailable")
	}
	return value, nil
}

// Put saves a bounded secret in the native store, never the application preferences database.
func (NativeStore) Put(ctx context.Context, id, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validId(id) || len(value) == 0 || len(value) > 2048 {
		return errors.New("invalid credential identifier or size")
	}
	if keyring.Set("Yousage", id, value) != nil {
		return errors.New("system credential store could not save the credential")
	}
	return nil
}
func (NativeStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validId(id) {
		return errors.New("invalid credential identifier")
	}
	err := keyring.Delete("Yousage", id)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if err != nil {
		return errors.New("system credential store could not remove the credential")
	}
	return nil
}
