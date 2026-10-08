package sqlite_test

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestDelegationPreviewMigrationRemovesLongLegacyAndNULCopies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-delegations.db")
	store := openStore(t, path)
	now := "2026-09-26T10:00:00Z"
	if _, err := store.DB().Exec(`INSERT INTO workspaces(id,path,profile,created_at) VALUES('workspace','/synthetic/workspace','ask',?)`, now); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"parent", "child-long", "child-nul"} {
		if _, err := store.DB().Exec(`INSERT INTO sessions(id,workspace_id,backend_id,status,created_at,updated_at) VALUES(?,'workspace','local','ready',?,?)`, id, now, now); err != nil {
			t.Fatal(err)
		}
	}
	long := strings.Repeat("ação ", 100)
	nul := "prefixo\x00" + strings.Repeat("conteúdo", 100)
	for _, item := range []struct{ id, child, task string }{{"link-long", "child-long", long}, {"link-nul", "child-nul", nul}} {
		if _, err := store.DB().Exec(`INSERT INTO session_delegations(id,parent_session_id,child_session_id,agent_id,depth,created_at,task_prompt) VALUES(?,'parent',?,'agent',1,?,?)`, item.id, item.child, now, item.task); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().Exec(`DELETE FROM schema_migrations WHERE version=19`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var gotLong, gotNUL string
	if err := reopened.DB().QueryRow(`SELECT task_prompt FROM session_delegations WHERE id='link-long'`).Scan(&gotLong); err != nil {
		t.Fatal(err)
	}
	if err := reopened.DB().QueryRow(`SELECT task_prompt FROM session_delegations WHERE id='link-nul'`).Scan(&gotNUL); err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(gotLong) != 281 || !strings.HasSuffix(gotLong, "…") {
		t.Fatalf("long legacy task remained in catalog: runes=%d", utf8.RuneCountInString(gotLong))
	}
	if strings.ContainsRune(gotNUL, '\x00') || strings.Contains(gotNUL, "conteúdo") {
		t.Fatal("NUL-containing legacy task remained in catalog")
	}
}
