package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/repositories"
	"github.com/persioflexa/harflex/internal/sdd"
)

// A pipeline works in one folder: <execution cache>/pipeline-<id> holds the Coder's private copy and the QA lab of
// the latest round. A newer copy or lab replaces the older one, and the whole folder goes once the PRs are accepted;
// the conversations are history in the catalog, so they outlive the folder.

const removingPrefix = ".removing-"

func (s *Service) executionCache() (string, error) {
	if s.executionCacheRoot != "" {
		return s.executionCacheRoot, nil
	}
	return legacyExecutionCache()
}

// legacyExecutionCache is where copies lived before they moved beside the catalog; what is left there is cleared
// like the rest.
func legacyExecutionCache() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "Harflex", "sdd-executions"), nil
}

// executionCaches lists every folder copies may live in, current first.
func (s *Service) executionCaches() []string {
	var caches []string
	if cache, err := s.executionCache(); err == nil {
		caches = append(caches, cache)
	}
	if legacy, err := legacyExecutionCache(); err == nil && (len(caches) == 0 || legacy != caches[0]) {
		caches = append(caches, legacy)
	}
	return caches
}

func pipelineWorkFolder(cache, pipelineID string) string {
	return filepath.Join(cache, "pipeline-"+pipelineID)
}

// prunePipelineCopies removes the private copies a pipeline no longer works in: every copy but the latest Coder's and
// every lab but the latest QA's, or, once the work is finished, all of them with the pipeline's folder. A copy whose
// conversation still runs, or that a local test was applied from, stays.
func (s *Service) prunePipelineCopies(ctx context.Context, pipelineID string, finished bool) error {
	cache, err := s.executionCache()
	if err != nil {
		return err
	}
	run, err := s.store.GetPipeline(ctx, pipelineID)
	if err != nil {
		return err
	}
	if test, err := s.store.ActivePipelineLocalTest(ctx, run.WorkspaceID); err == nil && test.PipelineID == pipelineID {
		return nil
	}
	keep := map[string]bool{}
	var doomed []string
	for _, role := range []string{"coder", "evaluator"} {
		links, err := s.store.ListPipelineSessions(ctx, pipelineID, role)
		if err != nil {
			return err
		}
		for index, link := range links {
			snapshot, err := s.executionSnapshotForLink(link)
			if err != nil {
				continue
			}
			root := snapshot.Root
			if role == "evaluator" {
				root = qaLabRoot(snapshot.Root, link.ID)
			}
			record, err := s.store.GetSession(ctx, link.SessionID)
			if (!finished && index == 0) || err != nil || record.Status == "running" {
				keep[root] = true
				continue
			}
			doomed = append(doomed, root)
		}
	}
	if finished && len(keep) == 0 {
		doomed = append(doomed, pipelineWorkFolder(cache, pipelineID))
	}
	var failures []error
	for _, path := range doomed {
		if keep[path] {
			continue
		}
		if err := s.removePrivateCopy(path); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// removePrivateCopy moves a copy out of the way at once and deletes it in the background. Only folders inside an
// execution cache are ever touched.
func (s *Service) removePrivateCopy(path string) error {
	cache := ""
	for _, candidate := range s.executionCaches() {
		if insideCache(candidate, path) {
			cache = candidate
			break
		}
	}
	if cache == "" {
		return nil
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	parked, err := os.MkdirTemp(cache, removingPrefix)
	if err != nil {
		return err
	}
	if err := os.Rename(path, filepath.Join(parked, filepath.Base(path))); err != nil {
		_ = os.Remove(parked)
		return err
	}
	s.removeInBackground(parked)
	return nil
}

// removeInBackground deletes a parked copy without holding anything up; when the app quits first, the next sweep
// finishes it.
func (s *Service) removeInBackground(path string) {
	go func() {
		if err := removeTree(s.ctx, path); err != nil && s.ctx.Err() == nil {
			s.diagnostics.write(map[string]any{"event": "copy.remove_failed", "detail": diagnosticText(err)})
		}
	}()
}

func insideCache(cache, path string) bool {
	for _, base := range []string{cache, resolvedPath(cache)} {
		for _, candidate := range []string{path, resolvedPath(path)} {
			relative, err := filepath.Rel(base, candidate)
			if err == nil && relative != "." && !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				return true
			}
		}
	}
	return false
}

func resolvedPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// removeTree deletes a tree depth first, stopping when the app quits, even where a dependency cache left folders
// read-only.
func removeTree(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		if info.Mode().Perm()&0o700 != 0o700 {
			_ = os.Chmod(path, 0o700)
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := removeTree(ctx, filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SweepPipelineCopies clears what earlier rounds and versions left in the execution cache: copies and labs a pipeline
// no longer works in, the folders of finished work, and removals cut short. Folders no pipeline names stay.
//
//wails:ignore
func (s *Service) SweepPipelineCopies() {
	if err := s.beginCall(); err != nil {
		return
	}
	defer s.endCall()
	for _, cache := range s.executionCaches() {
		entries, err := os.ReadDir(cache)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), removingPrefix) {
				s.removeInBackground(filepath.Join(cache, entry.Name()))
			}
		}
	}
	workspaces, err := s.store.ListWorkspaces(s.ctx)
	if err != nil {
		return
	}
	for _, workspace := range workspaces {
		runs, err := s.store.ListPipelines(s.ctx, workspace.ID)
		if err != nil {
			continue
		}
		for _, run := range runs {
			if err := s.prunePipelineCopies(s.ctx, run.ID, run.Status[sdd.PRs] == sdd.Completed); err != nil {
				s.diagnostics.write(map[string]any{"event": "copy.sweep_failed", "pipelineId": run.ID, "detail": diagnosticText(err)})
			}
		}
	}
}

// restoreMissingCopy rebuilds the Coder's copy from the change saved with the pipeline when its folder is gone, and
// keeps it only when it gives back exactly that change.
func (s *Service) restoreMissingCopy(snapshot repositories.ExecutionSnapshot, evidence string) error {
	if _, err := os.Lstat(snapshot.Root); !os.IsNotExist(err) {
		return nil
	}
	if evidence == "" {
		return ErrPipelineCodeSnapshotUnavailable
	}
	if err := repositories.RestoreExecutionSnapshot(s.ctx, snapshot, evidence, evidenceModes(evidence)); err != nil {
		s.diagnostics.write(map[string]any{"event": "copy.restore_failed", "detail": diagnosticText(err)})
		return ErrPipelineCodeSnapshotUnavailable
	}
	if current, _, err := pipelineCodeEvidence(s.ctx, snapshot, catalog.MaxExecutionEvidenceBytes); err != nil || current != evidence {
		_ = os.RemoveAll(snapshot.Root)
		s.diagnostics.write(map[string]any{"event": "copy.restore_mismatch"})
		return ErrPipelineCodeSnapshotUnavailable
	}
	return nil
}

var evidenceModeLine = regexp.MustCompile(`^# file=("(?:[^"\\]|\\.)*") action=\S+ before=\S+ mode=\S+ type=\S+ after=\S+ mode=([0-7]{4}) type=file$`)

// evidenceModes reads the final mode of each changed file from the evidence's provenance lines.
func evidenceModes(evidence string) map[string]os.FileMode {
	modes := map[string]os.FileMode{}
	for _, line := range strings.Split(evidence, "\n") {
		match := evidenceModeLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		path, err := strconv.Unquote(match[1])
		mode, modeErr := strconv.ParseUint(match[2], 8, 32)
		if err == nil && modeErr == nil {
			modes[path] = os.FileMode(mode)
		}
	}
	return modes
}
