package catalog

import "time"

// OpenRouterManagementCredential identifies the active catalog-only secret.
// The secret bytes remain in the system keyring; this record stores no key.
type OpenRouterManagementCredential struct {
	ProfileID          string
	Origin             string
	CredentialProvider string
	CredentialAccount  string
	UpdatedAt          time.Time
}
