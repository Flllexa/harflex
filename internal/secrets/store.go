package secrets

import (
	"context"
	"errors"
	"strings"
)

const maxReferenceComponentBytes = 255

type Reference struct {
	Provider string `json:"provider"`
	Account  string `json:"account"`
}

var ErrNotFound = errors.New("secret not found")

type Store interface {
	Put(context.Context, Reference, string) error
	Get(context.Context, Reference) (string, error)
	Delete(context.Context, Reference) error
}

func validateReference(ref Reference) error {
	if ref.Provider == "" || ref.Account == "" {
		return errors.New("provider and account are required")
	}
	if strings.Contains(ref.Provider, "/") || strings.Contains(ref.Account, "/") {
		return errors.New("provider and account must not contain slash")
	}
	if len(ref.Provider) > maxReferenceComponentBytes || len(ref.Account) > maxReferenceComponentBytes {
		return errors.New("provider or account exceeds byte limit")
	}
	return nil
}
