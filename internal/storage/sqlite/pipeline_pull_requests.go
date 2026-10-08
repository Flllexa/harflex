package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

const pullRequestColumns = "id, pipeline_id, session_id, url, title, branch, state, watch, last_checked_at, timeline, created_at, updated_at"

// MaxPullRequestTimeline keeps the most recent lines of a pull request's timeline.
const MaxPullRequestTimeline = 60

func scanPullRequest(scan func(...any) error) (catalog.PipelinePullRequest, error) {
	var pr catalog.PipelinePullRequest
	var watch int
	var checked, timeline, created, updated string
	if err := scan(&pr.ID, &pr.PipelineID, &pr.SessionID, &pr.URL, &pr.Title, &pr.Branch, &pr.State, &watch, &checked, &timeline, &created, &updated); err != nil {
		return pr, err
	}
	pr.Watch = watch == 1
	if checked != "" {
		pr.LastCheckedAt, _ = time.Parse(time.RFC3339Nano, checked)
	}
	_ = json.Unmarshal([]byte(timeline), &pr.Timeline)
	pr.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	pr.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return pr, nil
}

func encodeTimeline(events []catalog.PullRequestEvent) string {
	if len(events) > MaxPullRequestTimeline {
		events = events[len(events)-MaxPullRequestTimeline:]
	}
	if events == nil {
		events = []catalog.PullRequestEvent{}
	}
	encoded, _ := json.Marshal(events)
	return string(encoded)
}

// SavePipelinePullRequest inserts a pull request, or updates the one with the same pipeline and URL.
func (s *Store) SavePipelinePullRequest(ctx context.Context, pr catalog.PipelinePullRequest) (catalog.PipelinePullRequest, error) {
	watch := 0
	if pr.Watch {
		watch = 1
	}
	checked := ""
	if !pr.LastCheckedAt.IsZero() {
		checked = formatCatalogTime(pr.LastCheckedAt)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO pipeline_pull_requests (`+pullRequestColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(pipeline_id, url) DO UPDATE SET session_id=excluded.session_id, title=excluded.title, branch=excluded.branch, state=excluded.state,
		watch=excluded.watch, last_checked_at=excluded.last_checked_at, timeline=excluded.timeline, updated_at=excluded.updated_at`,
		pr.ID, pr.PipelineID, pr.SessionID, pr.URL, pr.Title, pr.Branch, pr.State, watch, checked, encodeTimeline(pr.Timeline), formatCatalogTime(pr.CreatedAt), formatCatalogTime(pr.UpdatedAt))
	if err != nil {
		return pr, fmt.Errorf("save pull request: %w", err)
	}
	return s.pullRequestByURL(ctx, pr.PipelineID, pr.URL)
}

func (s *Store) pullRequestByURL(ctx context.Context, pipelineID, url string) (catalog.PipelinePullRequest, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+pullRequestColumns+" FROM pipeline_pull_requests WHERE pipeline_id=? AND url=?", pipelineID, url)
	return scanPullRequest(row.Scan)
}

func (s *Store) GetPipelinePullRequest(ctx context.Context, id string) (catalog.PipelinePullRequest, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+pullRequestColumns+" FROM pipeline_pull_requests WHERE id=?", id)
	pr, err := scanPullRequest(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return pr, err
	}
	if err != nil {
		return pr, fmt.Errorf("get pull request: %w", err)
	}
	return pr, nil
}

func (s *Store) listPullRequests(ctx context.Context, query string, args ...any) ([]catalog.PipelinePullRequest, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+pullRequestColumns+" FROM pipeline_pull_requests "+query, args...)
	if err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}
	defer rows.Close()
	result := []catalog.PipelinePullRequest{}
	for rows.Next() {
		pr, err := scanPullRequest(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan pull request: %w", err)
		}
		result = append(result, pr)
	}
	return result, rows.Err()
}

func (s *Store) ListPipelinePullRequests(ctx context.Context, pipelineID string) ([]catalog.PipelinePullRequest, error) {
	return s.listPullRequests(ctx, "WHERE pipeline_id=? ORDER BY created_at, rowid", pipelineID)
}

// ListWatchedPullRequests returns the open pull requests whose review watch is on.
func (s *Store) ListWatchedPullRequests(ctx context.Context) ([]catalog.PipelinePullRequest, error) {
	return s.listPullRequests(ctx, "WHERE watch=1 AND state='open' ORDER BY last_checked_at, rowid")
}
