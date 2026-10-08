package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/storage/migrations"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(ctx context.Context, path string) (*Store, error) {
	normalizedPath := path
	if path != ":memory:" {
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve sqlite path %q: %w", path, err)
		}
		normalizedPath = filepath.ToSlash(absolutePath)
	}
	db, err := sql.Open("sqlite", sqliteDSN(normalizedPath))
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	success := false
	defer func() {
		if !success {
			_ = db.Close()
		}
	}()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping sqlite %q: %w", path, err)
	}
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON"} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return nil, fmt.Errorf("configure sqlite %q (%s): %w", path, pragma, err)
		}
	}
	if err := migrate(ctx, db); err != nil {
		return nil, fmt.Errorf("migrate sqlite %q: %w", path, err)
	}
	success = true
	return &Store{db: db}, nil
}

func sqliteDSN(normalizedPath string) string {
	params := url.Values{"_pragma": {"foreign_keys(ON)", "busy_timeout(5000)"}}
	if normalizedPath == ":memory:" {
		return normalizedPath + "?" + params.Encode()
	}
	if len(normalizedPath) >= 3 && normalizedPath[1:3] == ":/" &&
		((normalizedPath[0] >= 'A' && normalizedPath[0] <= 'Z') || (normalizedPath[0] >= 'a' && normalizedPath[0] <= 'z')) {
		normalizedPath = "/" + normalizedPath
	}
	uri := url.URL{Scheme: "file", Path: normalizedPath, RawQuery: params.Encode()}
	return uri.String()
}

func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close sqlite: %w", err)
	}
	return nil
}

func (s *Store) DB() *sql.DB { return s.db }

func migrate(ctx context.Context, db *sql.DB) error {
	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(files)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("create migration history: %w", err)
	}
	for _, name := range files {
		prefix, _, _ := strings.Cut(name, "_")
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return fmt.Errorf("parse migration %q version: %w", name, err)
		}
		var applied bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)", version).Scan(&applied); err != nil {
			return fmt.Errorf("read migration %q version: %w", name, err)
		}
		if applied {
			continue
		}
		contents, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %q: %w", name, err)
		}
		if version == 43 {
			if err := reconcileExecutionBaselineColumns(ctx, tx); err != nil {
				return fmt.Errorf("apply migration %q: %w", name, err)
			}
		}
		if _, err := tx.ExecContext(ctx, string(contents)); err != nil {
			return fmt.Errorf("apply migration %q: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", version, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record migration %q: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func reconcileExecutionBaselineColumns(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info('pipeline_sessions')`)
	if err != nil {
		return fmt.Errorf("inspect execution baseline schema: %w", err)
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read execution baseline schema: %w", err)
		}
		columns[name] = true
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if iterationErr != nil {
		return fmt.Errorf("iterate execution baseline schema: %w", iterationErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close execution baseline schema: %w", closeErr)
	}
	for _, required := range []struct{ name, statement string }{
		{"baseline_git_files", `ALTER TABLE pipeline_sessions ADD COLUMN baseline_git_files TEXT NOT NULL DEFAULT '[]'`},
		{"baseline_git_file_hashes", `ALTER TABLE pipeline_sessions ADD COLUMN baseline_git_file_hashes TEXT NOT NULL DEFAULT '{}'`},
	} {
		if !columns[required.name] {
			if _, err := tx.ExecContext(ctx, required.statement); err != nil {
				return fmt.Errorf("add execution baseline column %s: %w", required.name, err)
			}
		}
	}
	return nil
}
