-- The repository a pull request belongs to: "." for the project's own, or the path of a nested repository such as
-- domains/contracts, so a work that changed several repositories gets one pull request for each.
ALTER TABLE pipeline_pull_requests ADD COLUMN repository TEXT NOT NULL DEFAULT '';
