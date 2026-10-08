package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	moderncsqlite "modernc.org/sqlite"
)

// The test driver pauses after a SELECT has read its row, or immediately before
// the first pipeline write. This makes statement order observable without
// changing production code or relying on scheduler timing.
type snapshotStatementGate struct {
	once     sync.Once
	observed chan string
	release  chan struct{}
}

func newSnapshotStatementGate() *snapshotStatementGate {
	return &snapshotStatementGate{observed: make(chan string, 1), release: make(chan struct{})}
}

func (g *snapshotStatementGate) stop(statement string) {
	g.once.Do(func() {
		g.observed <- statement
		<-g.release
	})
}

type snapshotTraceDriver struct {
	base driver.Driver
	gate *snapshotStatementGate
}

func (d *snapshotTraceDriver) Open(name string) (driver.Conn, error) {
	raw, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	if _, ok := raw.(driver.ConnBeginTx); !ok {
		_ = raw.Close()
		return nil, errors.New("SQLite driver lacks ConnBeginTx")
	}
	if _, ok := raw.(driver.ExecerContext); !ok {
		_ = raw.Close()
		return nil, errors.New("SQLite driver lacks ExecerContext")
	}
	if _, ok := raw.(driver.QueryerContext); !ok {
		_ = raw.Close()
		return nil, errors.New("SQLite driver lacks QueryerContext")
	}
	return &snapshotTraceConn{raw: raw, gate: d.gate}, nil
}

type snapshotTraceConn struct {
	raw  driver.Conn
	gate *snapshotStatementGate
}

func (c *snapshotTraceConn) Prepare(query string) (driver.Stmt, error) { return c.raw.Prepare(query) }
func (c *snapshotTraceConn) Close() error                              { return c.raw.Close() }
func (c *snapshotTraceConn) Begin() (driver.Tx, error)                 { return c.raw.Begin() }
func (c *snapshotTraceConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.raw.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *snapshotTraceConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	statement := strings.TrimSpace(query)
	if strings.HasPrefix(statement, "UPDATE pipeline_runs") || strings.HasPrefix(statement, "INSERT INTO pipeline_runs") {
		c.gate.stop("write")
	}
	return c.raw.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *snapshotTraceConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.raw.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	statement := strings.TrimSpace(query)
	if strings.HasPrefix(statement, "SELECT kind") || strings.HasPrefix(statement, "SELECT "+pipelineColumns+" FROM pipeline_runs WHERE id = ?") || strings.HasPrefix(statement, "SELECT "+brainstormRunColumns+" FROM pipeline_brainstorm_runs WHERE id=?") {
		return &snapshotTraceRows{Rows: rows, gate: c.gate}, nil
	}
	return rows, nil
}

func TestCrossStoreBrainstormReadUsesOneSnapshot(t *testing.T) {
	for _, lookup := range []string{"run", "pipeline", "start request"} {
		t.Run(lookup, func(t *testing.T) {
			writer, path, run, start := brainstormFixture(t)
			gate := newSnapshotStatementGate()
			reader := openSnapshotTraceStore(t, path, gate)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			release := sync.OnceFunc(func() { close(gate.release) })
			defer release()
			type readResult struct {
				run catalog.BrainstormRun
				err error
			}
			result := make(chan readResult, 1)
			go func() {
				var got catalog.BrainstormRun
				var err error
				switch lookup {
				case "run":
					got, err = reader.GetBrainstorming(ctx, run.ID)
				case "pipeline":
					got, err = reader.GetBrainstormingByPipeline(ctx, run.PipelineID, run.DiscoveryVersion)
				default:
					got, err = reader.GetBrainstormingByStartRequest(ctx, run.PipelineID, start.RequestID)
				}
				result <- readResult{got, err}
			}()
			select {
			case statement := <-gate.observed:
				if statement != "read" {
					t.Fatalf("first statement: %s", statement)
				}
			case <-ctx.Done():
				t.Fatal("reader did not reach run snapshot")
			}
			if err := writer.ReviseAuthoringDiscovery(ctx, run.PipelineID, 2, 1, "Revised Discovery", "New title", "New objective"); err != nil {
				t.Fatal(err)
			}
			release()
			select {
			case got := <-result:
				if got.err != nil || got.run.PipelineRevision != 2 || got.run.Revision != 1 || got.run.State != "ready" || got.run.DiscoveryContent != run.DiscoveryContent {
					t.Fatalf("inconsistent read: %+v %v", got.run, got.err)
				}
			case <-ctx.Done():
				t.Fatal("reader did not finish")
			}
			latest, err := writer.GetBrainstorming(ctx, run.ID)
			if err != nil || latest.PipelineRevision != 3 || latest.State != "invalidated" || latest.Revision != 2 {
				t.Fatalf("write not committed: %+v %v", latest, err)
			}
		})
	}
}

type snapshotTraceRows struct {
	driver.Rows
	gate *snapshotStatementGate
}

func (r *snapshotTraceRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	if err == nil {
		r.gate.stop("read")
	}
	return err
}

var snapshotTraceDriverID atomic.Uint64

func openSnapshotTraceStore(t *testing.T, path string, gate *snapshotStatementGate) *Store {
	t.Helper()
	name := fmt.Sprintf("sqlite_snapshot_trace_%d", snapshotTraceDriverID.Add(1))
	sql.Register(name, &snapshotTraceDriver{base: &moderncsqlite.Driver{}, gate: gate})
	db, err := sql.Open(name, sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(t.Context()); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Store{db: db}
}

func runStaleSnapshotCall(t *testing.T, first *Store, gate *snapshotStatementGate, pipelineID string, call func(context.Context) error) (string, error) {
	t.Helper()
	writer, err := first.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback() }()
	if _, err := writer.ExecContext(t.Context(), `UPDATE pipeline_runs SET revision = revision + 1 WHERE id = ?`, pipelineID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- call(ctx) }()
	var firstStatement string
	select {
	case firstStatement = <-gate.observed:
	case <-ctx.Done():
		_ = writer.Rollback()
		close(gate.release)
		t.Fatal("second Store did not reach its first pipeline statement")
	}
	commitErr := writer.Commit()
	close(gate.release)
	if commitErr != nil {
		t.Fatal(commitErr)
	}
	select {
	case err := <-result:
		return firstStatement, err
	case <-ctx.Done():
		t.Fatal("second Store did not return after writer committed")
		return "", nil
	}
}

func TestCrossStorePipelineWritesStartWithConditionalWrite(t *testing.T) {
	for _, operation := range []string{"artifact", "transition"} {
		t.Run(operation, func(t *testing.T) {
			first, path, run := authoringFixture(t)
			run.ID, run.Kind = "legacy", "legacy"
			if err := first.CreatePipeline(t.Context(), run); err != nil {
				t.Fatal(err)
			}
			gate := newSnapshotStatementGate()
			second := openSnapshotTraceStore(t, path, gate)
			next, err := sdd.Advance(sdd.Flow{Current: run.Current, Status: run.Status}, true)
			if err != nil {
				t.Fatal(err)
			}
			firstStatement, err := runStaleSnapshotCall(t, first, gate, run.ID, func(ctx context.Context) error {
				if operation == "artifact" {
					return second.SavePipelineArtifact(ctx, run.ID, sdd.Discovery, "stale content", 1)
				}
				return second.TransitionPipeline(ctx, run.ID, sdd.Discovery, next, "advance", "", 1)
			})
			if firstStatement != "write" || !errors.Is(err, ErrPipelineConflict) {
				t.Fatalf("first statement = %q, result = %v; want write and conflict", firstStatement, err)
			}
			got, err := first.GetPipeline(t.Context(), run.ID)
			if err != nil || got.Revision != 2 || got.Current != sdd.Discovery || len(got.Artifacts) != 0 {
				t.Fatalf("stale call changed pipeline: %+v, %v", got, err)
			}
			var events, transitions int
			if err := first.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM events WHERE stream_id = ?`, run.ID).Scan(&events); err != nil || events != 1 {
				t.Fatalf("stale call events = %d, %v", events, err)
			}
			if err := first.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pipeline_transitions WHERE pipeline_id = ?`, run.ID).Scan(&transitions); err != nil || transitions != 0 {
				t.Fatalf("stale call transitions = %d, %v", transitions, err)
			}
		})
	}
}

func TestCrossStoreDerivationStartsWithConditionalInsert(t *testing.T) {
	first, path, parent := authoringFixture(t)
	if err := first.CreateAuthoringPipeline(t.Context(), parent, "Original Discovery"); err != nil {
		t.Fatal(err)
	}
	if err := first.FreezeDiscovery(t.Context(), parent.ID, 1, 1); err != nil {
		t.Fatal(err)
	}
	gate := newSnapshotStatementGate()
	second := openSnapshotTraceStore(t, path, gate)
	child := catalog.PipelineRun{ID: "stale-child", WorkspaceID: parent.WorkspaceID, Kind: "ai_authoring", DerivedFromPipelineID: parent.ID,
		Title: "Derived", Objective: "Derived objective", Current: parent.Current, Status: parent.Status, Revision: 1, CreatedAt: parent.CreatedAt, UpdatedAt: parent.UpdatedAt}
	firstStatement, err := runStaleSnapshotCall(t, first, gate, parent.ID, func(ctx context.Context) error {
		return second.DeriveAuthoringPipeline(ctx, parent.ID, 2, child, "New Discovery")
	})
	if firstStatement != "write" || !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("first statement = %q, result = %v; want write and conflict", firstStatement, err)
	}
	parentAfter, err := first.GetPipeline(t.Context(), parent.ID)
	if err != nil || parentAfter.Revision != 3 || parentAfter.DiscoveryFrozenVersion != 1 || parentAfter.Artifacts[sdd.Discovery].Content != "Original Discovery" {
		t.Fatalf("stale derivation changed parent: %+v, %v", parentAfter, err)
	}
	if _, err := first.GetPipeline(t.Context(), child.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale child exists: %v", err)
	}
	var streams, events int
	if err := first.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM streams WHERE id = ?`, child.ID).Scan(&streams); err != nil || streams != 0 {
		t.Fatalf("stale child stream = %d, %v", streams, err)
	}
	if err := first.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM events WHERE stream_id = ?`, child.ID).Scan(&events); err != nil || events != 0 {
		t.Fatalf("stale child events = %d, %v", events, err)
	}
}

func TestCrossStorePipelineReadUsesOneSnapshot(t *testing.T) {
	for _, lookup := range []string{"pipeline ID", "authoring request"} {
		t.Run(lookup, func(t *testing.T) {
			writer, path, run := authoringFixture(t)
			run.CreationRequestID = "consistent-read-request"
			run.CreationRequestHash = "original-request-hash"
			if err := writer.CreateAuthoringPipeline(t.Context(), run, "Original Discovery"); err != nil {
				t.Fatal(err)
			}
			gate := newSnapshotStatementGate()
			reader := openSnapshotTraceStore(t, path, gate)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			release := sync.OnceFunc(func() { close(gate.release) })
			defer release()
			type readResult struct {
				run catalog.PipelineRun
				err error
			}
			result := make(chan readResult, 1)
			go func() {
				var got catalog.PipelineRun
				var err error
				if lookup == "pipeline ID" {
					got, err = reader.GetPipeline(ctx, run.ID)
				} else {
					got, err = reader.GetAuthoringPipelineByRequest(ctx, run.WorkspaceID, "", run.CreationRequestID)
				}
				result <- readResult{run: got, err: err}
			}()
			select {
			case statement := <-gate.observed:
				if statement != "read" {
					t.Fatalf("first observed statement = %q, want pipeline read", statement)
				}
			case <-ctx.Done():
				t.Fatal("reader did not reach the pipeline row")
			}
			if err := writer.ReviseAuthoringDiscovery(ctx, run.ID, 1, 1, "Revised Discovery", "Revised title", "Revised objective"); err != nil {
				t.Fatalf("concurrent Discovery revision: %v", err)
			}
			release()
			select {
			case read := <-result:
				if read.err != nil {
					t.Fatal(read.err)
				}
				artifact := read.run.Artifacts[sdd.Discovery]
				if read.run.Revision != 1 || read.run.Title != run.Title || read.run.Objective != run.Objective || artifact.Version != 1 || artifact.Content != "Original Discovery" {
					t.Fatalf("pipeline read crossed revisions: revision=%d title=%q objective=%q Discovery version=%d content=%q", read.run.Revision, read.run.Title, read.run.Objective, artifact.Version, artifact.Content)
				}
			case <-ctx.Done():
				t.Fatal("reader did not finish after the revision committed")
			}
			latest, err := writer.GetPipeline(ctx, run.ID)
			if err != nil || latest.Revision != 2 || latest.Artifacts[sdd.Discovery].Version != 2 {
				t.Fatalf("writer revision not visible: revision=%d Discovery version=%d, err=%v", latest.Revision, latest.Artifacts[sdd.Discovery].Version, err)
			}
		})
	}
}
