package secrets

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

// Byte limits keep native credentials bounded before starting backend processes.
const (
	maxServiceBytes       = 255
	maxSecretBytes        = 2048
	maxNativeCommandBytes = 4096
)

type keyringBackend interface {
	Set(service, account, secret string) error
	Get(service, account string) (string, error)
	Delete(service, account string) error
}

type nativeBackend struct{}

func (nativeBackend) Set(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

func (nativeBackend) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (nativeBackend) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

type keyringStore struct {
	service string
	backend keyringBackend
}

func NewKeyring(service string) Store {
	return &keyringStore{service: service, backend: nativeBackend{}}
}

func (k *keyringStore) validate(ctx context.Context, ref Reference) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if k.service == "" {
		return errors.New("keyring service is required")
	}
	if len(k.service) > maxServiceBytes {
		return errors.New("keyring service exceeds byte limit")
	}
	return validateReference(ref)
}

type operationError struct {
	operation string
	ref       Reference
	cause     error
}

func (e *operationError) Error() string {
	return fmt.Sprintf("keyring %s %s/%s failed", e.operation, e.ref.Provider, e.ref.Account)
}

func (e *operationError) Unwrap() error { return e.cause }

func wrapError(operation string, ref Reference, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, keyring.ErrNotFound) {
		err = ErrNotFound
	}
	return &operationError{operation: operation, ref: ref, cause: err}
}

func validateSecretSize(service string, ref Reference, secret string) error {
	if len(secret) > maxSecretBytes {
		return errors.New("secret exceeds byte limit")
	}
	// go-keyring v0.2.8 starts security before checking the escaped command size.
	// Bound quote expansion and base64 output first to avoid that unclosed process.
	quotedSize := func(value string) int {
		return len(value) + 4*strings.Count(value, "'") + 2
	}
	commandSize := len("add-generic-password -U -s  -a  -w \n") +
		quotedSize(service) + quotedSize(ref.Provider+"/"+ref.Account) +
		len("go-keyring-base64:") + base64.StdEncoding.EncodedLen(len(secret)) + 2
	if commandSize > maxNativeCommandBytes {
		return errors.New("credential exceeds native command byte limit")
	}
	return nil
}

// Native keyring calls are synchronous; cancellation is checked before each call.
func (k *keyringStore) Put(ctx context.Context, ref Reference, secret string) error {
	if err := k.validate(ctx, ref); err != nil {
		return wrapError("put", ref, err)
	}
	if err := validateSecretSize(k.service, ref, secret); err != nil {
		return wrapError("put", ref, err)
	}
	return wrapError("put", ref, k.backend.Set(k.service, ref.Provider+"/"+ref.Account, secret))
}

func (k *keyringStore) Get(ctx context.Context, ref Reference) (string, error) {
	if err := k.validate(ctx, ref); err != nil {
		return "", wrapError("get", ref, err)
	}
	secret, err := k.backend.Get(k.service, ref.Provider+"/"+ref.Account)
	if err != nil {
		return "", wrapError("get", ref, err)
	}
	return secret, nil
}

// Delete returns ErrNotFound when the native backend reports a missing secret.
func (k *keyringStore) Delete(ctx context.Context, ref Reference) error {
	if err := k.validate(ctx, ref); err != nil {
		return wrapError("delete", ref, err)
	}
	return wrapError("delete", ref, k.backend.Delete(k.service, ref.Provider+"/"+ref.Account))
}
