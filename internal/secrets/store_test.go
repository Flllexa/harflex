package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

const secretFixture = "test-secret-never-serialize"

type backendCall struct {
	operation string
	service   string
	account   string
}

type memoryBackend struct {
	values map[string]string
	calls  []backendCall
	err    error
}

func (b *memoryBackend) Set(service, account, secret string) error {
	b.calls = append(b.calls, backendCall{"put", service, account})
	if b.err != nil {
		return b.err
	}
	b.values[service+"\x00"+account] = secret
	return nil
}

func (b *memoryBackend) Get(service, account string) (string, error) {
	b.calls = append(b.calls, backendCall{"get", service, account})
	if b.err != nil {
		return secretFixture, b.err
	}
	secret, ok := b.values[service+"\x00"+account]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return secret, nil
}

func (b *memoryBackend) Delete(service, account string) error {
	b.calls = append(b.calls, backendCall{"delete", service, account})
	if b.err != nil {
		return b.err
	}
	key := service + "\x00" + account
	if _, ok := b.values[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(b.values, key)
	return nil
}

func testStore() (*keyringStore, *memoryBackend) {
	backend := &memoryBackend{values: make(map[string]string)}
	return &keyringStore{service: "ai.harflex.desktop", backend: backend}, backend
}

func TestReferenceJSONContainsOnlyIdentifiers(t *testing.T) {
	reference := Reference{Provider: "provider", Account: "account"}
	store, _ := testStore()
	if err := store.Put(context.Background(), reference, secretFixture); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(reference)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"provider":"provider","account":"account"}` {
		t.Fatalf("unexpected reference JSON: %s", data)
	}
	if strings.Contains(string(data), secretFixture) {
		t.Fatal("reference JSON contains secret")
	}
}

func TestKeyringRoundTripAndOverwrite(t *testing.T) {
	store, backend := testStore()
	var contract Store = store
	ctx := context.Background()
	reference := Reference{Provider: "provider", Account: "account"}
	for _, secret := range []string{secretFixture, "replacement-secret"} {
		if err := contract.Put(ctx, reference, secret); err != nil {
			t.Fatal(err)
		}
		got, err := contract.Get(ctx, reference)
		if err != nil || got != secret {
			t.Fatalf("secret round trip failed: %v", err)
		}
	}
	if err := contract.Delete(ctx, reference); err != nil {
		t.Fatal(err)
	}
	if got, err := contract.Get(ctx, reference); !errors.Is(err, ErrNotFound) || got != "" {
		t.Fatalf("Get after Delete must return empty value and ErrNotFound: %v", err)
	}
	if err := contract.Delete(ctx, reference); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete of missing reference must return ErrNotFound: %v", err)
	}
	wantOperations := []string{"put", "get", "put", "get", "delete", "get", "delete"}
	if len(backend.calls) != len(wantOperations) {
		t.Fatalf("got %d backend calls, want %d", len(backend.calls), len(wantOperations))
	}
	for i, call := range backend.calls {
		want := backendCall{wantOperations[i], "ai.harflex.desktop", "provider/account"}
		if call != want {
			t.Fatalf("call %d = %+v, want %+v", i, call, want)
		}
	}
}

func invokeStore(t *testing.T, store Store, ctx context.Context, ref Reference, operation string) error {
	t.Helper()
	switch operation {
	case "put":
		return store.Put(ctx, ref, secretFixture)
	case "get":
		secret, err := store.Get(ctx, ref)
		if err != nil && secret != "" {
			t.Fatal("Get returned secret on failure")
		}
		return err
	case "delete":
		return store.Delete(ctx, ref)
	default:
		t.Fatalf("unknown operation %q", operation)
		return nil
	}
}

func TestKeyringRejectsCanceledContextBeforeBackend(t *testing.T) {
	for _, operation := range []string{"put", "get", "delete"} {
		t.Run(operation, func(t *testing.T) {
			store, backend := testStore()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := invokeStore(t, store, ctx, Reference{Provider: "provider", Account: "account"}, operation)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want context.Canceled", err)
			}
			if len(backend.calls) != 0 {
				t.Fatal("canceled operation reached backend")
			}
		})
	}
}

func TestKeyringValidatesConfigurationBeforeBackend(t *testing.T) {
	for _, tc := range []struct {
		name    string
		service string
		ref     Reference
	}{
		{"empty service", "", Reference{Provider: "provider", Account: "account"}},
		{"empty provider", "service", Reference{Account: "account"}},
		{"empty account", "service", Reference{Provider: "provider"}},
		{"slash in provider", "service", Reference{Provider: "a/b", Account: "c"}},
		{"slash in account", "service", Reference{Provider: "a", Account: "b/c"}},
		{"oversized service", strings.Repeat("s", 256), Reference{Provider: "provider", Account: "account"}},
		{"oversized provider", "service", Reference{Provider: strings.Repeat("p", 256), Account: "account"}},
		{"oversized account", "service", Reference{Provider: "provider", Account: strings.Repeat("a", 256)}},
		{"oversized multibyte provider", "service", Reference{Provider: strings.Repeat("é", 128), Account: "account"}},
	} {
		for _, operation := range []string{"put", "get", "delete"} {
			t.Run(tc.name+"/"+operation, func(t *testing.T) {
				store, backend := testStore()
				store.service = tc.service
				err := invokeStore(t, store, context.Background(), tc.ref, operation)
				if err == nil || strings.Contains(err.Error(), secretFixture) {
					t.Fatal("expected validation error without secret")
				}
				if len(backend.calls) != 0 {
					t.Fatal("invalid configuration reached backend")
				}
			})
		}
	}
}

func TestKeyringRejectsOversizedSecretBeforeBackend(t *testing.T) {
	for _, secret := range []string{strings.Repeat("s", 2049), strings.Repeat("é", 1025)} {
		store, backend := testStore()
		err := store.Put(context.Background(), Reference{Provider: "provider", Account: "account"}, secret)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatal("expected size validation error without secret")
		}
		if len(backend.calls) != 0 {
			t.Fatal("oversized secret reached backend")
		}
	}
}

func TestKeyringAcceptsValuesAtSizeLimits(t *testing.T) {
	store, backend := testStore()
	store.service = strings.Repeat("s", 255)
	ref := Reference{Provider: strings.Repeat("p", 255), Account: strings.Repeat("a", 255)}
	secret := strings.Repeat("s", 2048)
	ctx := context.Background()
	if err := store.Put(ctx, ref, secret); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get(ctx, ref); err != nil || got != secret {
		t.Fatalf("boundary value round trip failed: %v", err)
	}
	if err := store.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if len(backend.calls) != 3 {
		t.Fatalf("got %d backend calls, want 3", len(backend.calls))
	}
}

func TestKeyringRejectsOversizedEscapedNativeCommandBeforeBackend(t *testing.T) {
	store, backend := testStore()
	store.service = strings.Repeat("'", 255)
	ref := Reference{Provider: strings.Repeat("'", 255), Account: strings.Repeat("'", 255)}
	if err := store.Put(context.Background(), ref, strings.Repeat("s", 2048)); err == nil {
		t.Fatal("expected escaped native command size rejection")
	}
	if len(backend.calls) != 0 {
		t.Fatal("oversized escaped native command reached backend")
	}
}

func TestKeyringDistinctValidReferencesDoNotCollide(t *testing.T) {
	store, backend := testStore()
	ctx := context.Background()
	refs := []Reference{{Provider: "ab", Account: "c"}, {Provider: "a", Account: "bc"}}
	for i, ref := range refs {
		if err := store.Put(ctx, ref, fmt.Sprintf("secret-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if backend.calls[0].account == backend.calls[1].account {
		t.Fatal("distinct references share a backend key")
	}
	for i, ref := range refs {
		if got, err := store.Get(ctx, ref); err != nil || got != fmt.Sprintf("secret-%d", i) {
			t.Fatalf("distinct reference round trip failed: %v", err)
		}
	}
}

type sensitiveBackendError struct {
	message string
}

func (e *sensitiveBackendError) Error() string { return e.message }

func TestKeyringRedactsBackendErrorPreservingCause(t *testing.T) {
	const secret = "super-secret-value"
	cause := &sensitiveBackendError{message: "backend rejected " + secret}
	for _, operation := range []string{"put", "get", "delete"} {
		t.Run(operation, func(t *testing.T) {
			store, backend := testStore()
			backend.err = cause
			err := invokeStore(t, store, context.Background(), Reference{Provider: "provider", Account: "account"}, operation)
			if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), cause.Error()) {
				t.Fatal("error must redact backend message")
			}
			if !errors.Is(err, cause) {
				t.Fatal("redacted error lost original cause")
			}
			var typedCause *sensitiveBackendError
			if !errors.As(err, &typedCause) || typedCause != cause {
				t.Fatal("redacted error lost backend error type")
			}
		})
	}
}

func TestKeyringWrapsBackendErrors(t *testing.T) {
	backendFailure := errors.New("backend unavailable")
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"not found", keyring.ErrNotFound, ErrNotFound},
		{"wrapped not found", fmt.Errorf("backend: %w", keyring.ErrNotFound), ErrNotFound},
		{"backend failure", backendFailure, backendFailure},
	} {
		for _, operation := range []string{"put", "get", "delete"} {
			t.Run(tc.name+"/"+operation, func(t *testing.T) {
				store, backend := testStore()
				backend.err = tc.err
				err := invokeStore(t, store, context.Background(), Reference{Provider: "provider", Account: "account"}, operation)
				if !errors.Is(err, tc.want) {
					t.Fatalf("got %v, want wrapped %v", err, tc.want)
				}
				for _, expected := range []string{operation, "provider/account"} {
					if !strings.Contains(err.Error(), expected) {
						t.Fatalf("error %q missing operation/reference context %q", err, expected)
					}
				}
				if strings.Contains(err.Error(), secretFixture) {
					t.Fatal("error contains secret")
				}
			})
		}
	}
}

func TestNewKeyringWiresNativeBackendWithoutAccess(t *testing.T) {
	store, ok := NewKeyring("ai.harflex.desktop").(*keyringStore)
	if !ok || store.service != "ai.harflex.desktop" || store.backend == nil {
		t.Fatal("constructor did not configure native keyring")
	}
	for _, operation := range []string{"put", "get", "delete"} {
		t.Run(operation, func(t *testing.T) {
			err := invokeStore(t, NewKeyring(""), context.Background(), Reference{Provider: "provider", Account: "account"}, operation)
			if err == nil {
				t.Fatal("empty service must fail before accessing native keyring")
			}
		})
	}
}
