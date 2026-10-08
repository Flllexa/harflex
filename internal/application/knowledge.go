package application

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/knowledge"
)

var ErrKnowledgeNotFound = errors.New("knowledge document not found")
var ErrKnowledgeSourceUnavailable = errors.New("knowledge source unavailable")
var ErrKnowledgeSourceChanged = errors.New("knowledge source changed during import")

func knowledgeDocumentDTO(item catalog.KnowledgeDocument) KnowledgeDocumentDTO {
	return KnowledgeDocumentDTO{ID: item.ID, WorkspaceID: item.WorkspaceID, Path: item.Path, SourceSize: item.SourceSize, SourceModifiedAt: item.SourceModifiedAt, IndexedAt: item.IndexedAt, ChunkCount: item.ChunkCount}
}

func knowledgeHitDTO(item catalog.KnowledgeHit) KnowledgeHitDTO {
	return KnowledgeHitDTO{DocumentID: item.DocumentID, Path: item.Path, LineStart: item.LineStart, Snippet: item.Snippet, SourceModifiedAt: item.SourceModifiedAt, IndexedAt: item.IndexedAt}
}

func (s *Service) knowledgeWorkspace(id string) (catalog.Workspace, error) {
	if id == "" {
		return catalog.Workspace{}, ErrInvalidInput
	}
	workspace, err := s.store.GetWorkspace(s.ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Workspace{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return catalog.Workspace{}, safe("get knowledge workspace", err)
	}
	return workspace, nil
}

func relativeKnowledgePath(root, requested string) (string, error) {
	if requested == "" || strings.TrimSpace(requested) != requested || strings.ContainsRune(requested, 0) || strings.HasSuffix(requested, "/") || strings.HasSuffix(requested, `\`) {
		return "", ErrInvalidInput
	}
	path := requested
	if filepath.IsAbs(path) {
		var err error
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", ErrKnowledgeSourceUnavailable
		}
		path, err = filepath.Rel(root, path)
		if err != nil {
			return "", ErrInvalidInput
		}
	}
	if !filepath.IsLocal(path) || path == "." {
		return "", ErrInvalidInput
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".markdown":
		return filepath.Clean(path), nil
	default:
		return "", ErrInvalidInput
	}
}

func readKnowledgeSource(rootPath, relative string) ([]byte, time.Time, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, time.Time{}, ErrKnowledgeSourceUnavailable
	}
	defer root.Close()
	file, err := root.Open(relative)
	if err != nil {
		return nil, time.Time{}, ErrKnowledgeSourceUnavailable
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return nil, time.Time{}, ErrKnowledgeSourceUnavailable
	}
	if before.Size() > knowledge.MaxDocumentBytes {
		return nil, time.Time{}, ErrInvalidInput
	}
	content, err := io.ReadAll(io.LimitReader(file, knowledge.MaxDocumentBytes+1))
	if err != nil {
		return nil, time.Time{}, ErrKnowledgeSourceUnavailable
	}
	if len(content) > knowledge.MaxDocumentBytes || !utf8.Valid(content) || strings.IndexByte(string(content), 0) >= 0 || strings.TrimSpace(string(content)) == "" {
		return nil, time.Time{}, ErrInvalidInput
	}
	after, err := file.Stat()
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || int64(len(content)) != after.Size() {
		return nil, time.Time{}, ErrKnowledgeSourceChanged
	}
	return content, after.ModTime().UTC(), nil
}

func (s *Service) importKnowledge(workspace catalog.Workspace, requested string) (KnowledgeDocumentDTO, error) {
	relative, err := relativeKnowledgePath(workspace.Path, requested)
	if err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	content, modified, err := readKnowledgeSource(workspace.Path, relative)
	if err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	chunks := knowledge.ChunkText(string(content))
	if len(chunks) == 0 {
		return KnowledgeDocumentDTO{}, ErrInvalidInput
	}
	item := catalog.KnowledgeDocument{ID: id.New(), WorkspaceID: workspace.ID, Path: relative, ContentHash: fmt.Sprintf("%x", sha256.Sum256(content)), SourceSize: int64(len(content)), SourceModifiedAt: modified, IndexedAt: time.Now().UTC(), ChunkCount: len(chunks)}
	item, err = s.store.ReplaceKnowledge(s.ctx, item, chunks)
	if err != nil {
		return KnowledgeDocumentDTO{}, safe("index knowledge source", err)
	}
	return knowledgeDocumentDTO(item), nil
}

func (s *Service) ImportKnowledge(in ImportKnowledgeInput) (KnowledgeDocumentDTO, error) {
	if err := s.beginCall(); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	defer s.endCall()
	workspace, err := s.knowledgeWorkspace(in.WorkspaceID)
	if err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	return s.importKnowledge(workspace, in.Path)
}

func (s *Service) ReindexKnowledge(in KnowledgeDocumentInput) (KnowledgeDocumentDTO, error) {
	if err := s.beginCall(); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	defer s.endCall()
	workspace, err := s.knowledgeWorkspace(in.WorkspaceID)
	if err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	if in.DocumentID == "" {
		return KnowledgeDocumentDTO{}, ErrInvalidInput
	}
	item, err := s.store.GetKnowledge(s.ctx, workspace.ID, in.DocumentID)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeDocumentDTO{}, ErrKnowledgeNotFound
	}
	if err != nil {
		return KnowledgeDocumentDTO{}, safe("get knowledge document", err)
	}
	return s.importKnowledge(workspace, item.Path)
}

func (s *Service) ListKnowledge(workspaceID string) ([]KnowledgeDocumentDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if _, err := s.knowledgeWorkspace(workspaceID); err != nil {
		return nil, err
	}
	items, err := s.store.ListKnowledge(s.ctx, workspaceID)
	if err != nil {
		return nil, safe("list knowledge documents", err)
	}
	result := make([]KnowledgeDocumentDTO, 0, len(items))
	for _, item := range items {
		result = append(result, knowledgeDocumentDTO(item))
	}
	return result, nil
}

func knowledgeFTSQuery(input string) (string, error) {
	if len(input) > 256 || strings.TrimSpace(input) == "" {
		return "", ErrInvalidInput
	}
	terms := strings.FieldsFunc(input, func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character) && !unicode.IsMark(character)
	})
	if len(terms) == 0 || len(terms) > 12 {
		return "", ErrInvalidInput
	}
	for index, term := range terms {
		terms[index] = `"` + term + `"`
	}
	return strings.Join(terms, " AND "), nil
}

func (s *Service) SearchKnowledge(in SearchKnowledgeInput) ([]KnowledgeHitDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if _, err := s.knowledgeWorkspace(in.WorkspaceID); err != nil {
		return nil, err
	}
	query, err := knowledgeFTSQuery(in.Query)
	if err != nil || in.Limit < 0 || in.Limit > 50 {
		return nil, ErrInvalidInput
	}
	limit := in.Limit
	if limit == 0 {
		limit = 20
	}
	items, err := s.store.SearchKnowledge(s.ctx, in.WorkspaceID, in.DocumentID, query, limit)
	if err != nil {
		return nil, safe("search knowledge", err)
	}
	result := make([]KnowledgeHitDTO, 0, len(items))
	for _, item := range items {
		result = append(result, knowledgeHitDTO(item))
	}
	return result, nil
}

func (s *Service) RemoveKnowledge(in KnowledgeDocumentInput) error {
	if err := s.beginCall(); err != nil {
		return err
	}
	defer s.endCall()
	if in.WorkspaceID == "" || in.DocumentID == "" {
		return ErrInvalidInput
	}
	if err := s.store.RemoveKnowledge(s.ctx, in.WorkspaceID, in.DocumentID); errors.Is(err, sql.ErrNoRows) {
		return ErrKnowledgeNotFound
	} else if err != nil {
		return safe("remove knowledge document", err)
	}
	return nil
}
