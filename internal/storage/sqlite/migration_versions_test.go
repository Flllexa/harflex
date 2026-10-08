package sqlite

import (
	"io/fs"
	"strconv"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/storage/migrations"
)

// lastMigrationVersion is the highest version the embedded catalog schema defines. Tests that stand in for
// an installed catalog record every version up to it, so a new migration does not have to edit them.
func lastMigrationVersion(t testing.TB) int {
	t.Helper()
	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("read migrations: %d files, %v", len(files), err)
	}
	last := 0
	for _, name := range files {
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			t.Fatalf("migration %q has no numeric prefix: %v", name, err)
		}
		last = max(last, version)
	}
	return last
}
