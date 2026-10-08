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
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
)

var ErrSkillNotFound = errors.New("skill not found")
var ErrSkillImportFailed = errors.New("skill import failed")

func (s *Service) ImportSkill(in ImportSkillInput) (SkillDTO, error) {
	if err := s.beginCall(); err != nil {
		return SkillDTO{}, err
	}
	defer s.endCall()
	if in.WorkspaceID == "" || in.Path == "" || len(in.Path) > 4096 || filepath.Base(in.Path) != "SKILL.md" {
		return SkillDTO{}, ErrInvalidInput
	}
	workspace, err := s.store.GetWorkspace(s.ctx, in.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return SkillDTO{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return SkillDTO{}, safe("get skill workspace", err)
	}
	path := in.Path
	if filepath.IsAbs(path) {
		path, err = filepath.Rel(workspace.Path, path)
		if err != nil {
			return SkillDTO{}, ErrSkillImportFailed
		}
	}
	root, err := os.OpenRoot(workspace.Path)
	if err != nil {
		return SkillDTO{}, safe("open skill root", ErrSkillImportFailed)
	}
	defer root.Close()
	file, err := root.Open(path)
	if err != nil {
		return SkillDTO{}, safe("open skill file", ErrSkillImportFailed)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return SkillDTO{}, ErrSkillImportFailed
	}
	data, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil || len(data) > 64*1024 || !utf8.Valid(data) {
		return SkillDTO{}, ErrSkillImportFailed
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return SkillDTO{}, ErrSkillImportFailed
	}
	name := filepath.Base(filepath.Dir(path))
	description := ""
	if strings.HasPrefix(content, "---\n") {
		if end := strings.Index(content[4:], "\n---"); end >= 0 {
			for _, line := range strings.Split(content[4:4+end], "\n") {
				key, value, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				value = strings.Trim(strings.TrimSpace(value), "\"'")
				switch strings.TrimSpace(key) {
				case "name":
					if value != "" {
						name = value
					}
				case "description":
					description = value
				}
			}
		}
	}
	if name == "" || len(name) > 128 || len(description) > 500 {
		return SkillDTO{}, ErrSkillImportFailed
	}
	key := sha256.Sum256([]byte(in.WorkspaceID + "\x00" + filepath.Clean(path)))
	now := time.Now().UTC()
	item := catalog.Skill{ID: fmt.Sprintf("skillimport_%x", key[:12]), WorkspaceID: in.WorkspaceID, Name: name, Description: description, Content: content, Enabled: in.Enabled, Revision: 1, CreatedAt: now, UpdatedAt: now}
	previous, err := s.store.GetSkill(s.ctx, item.ID)
	if err == nil {
		if previous.WorkspaceID != in.WorkspaceID {
			return SkillDTO{}, ErrSkillImportFailed
		}
		item.CreatedAt, item.Revision = previous.CreatedAt, previous.Revision+1
	} else if !errors.Is(err, sql.ErrNoRows) {
		return SkillDTO{}, safe("lookup imported skill", err)
	}
	if err := s.store.UpsertSkill(s.ctx, item); err != nil {
		return SkillDTO{}, safe("import skill", err)
	}
	return skillDTO(item), nil
}

func skillDTO(item catalog.Skill) SkillDTO {
	return SkillDTO{ID: item.ID, WorkspaceID: item.WorkspaceID, Name: item.Name, Description: item.Description, Content: item.Content, Enabled: item.Enabled, Revision: item.Revision, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func (s *Service) SaveSkill(in SaveSkillInput) (SkillDTO, error) {
	if err := s.beginCall(); err != nil {
		return SkillDTO{}, err
	}
	defer s.endCall()
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.Content = strings.TrimSpace(in.Content)
	if in.WorkspaceID == "" || in.Name == "" || len(in.Name) > 128 || len(in.Description) > 500 || in.Content == "" || len(in.Content) > 64*1024 {
		return SkillDTO{}, ErrInvalidInput
	}
	if _, err := s.store.GetWorkspace(s.ctx, in.WorkspaceID); errors.Is(err, sql.ErrNoRows) {
		return SkillDTO{}, ErrWorkspaceNotFound
	} else if err != nil {
		return SkillDTO{}, safe("get skill workspace", err)
	}
	now := time.Now().UTC()
	item := catalog.Skill{ID: in.ID, WorkspaceID: in.WorkspaceID, Name: in.Name, Description: in.Description, Content: in.Content, Enabled: in.Enabled, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if in.ID == "" {
		item.ID = id.New()
	} else {
		previous, err := s.store.GetSkill(s.ctx, in.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return SkillDTO{}, ErrSkillNotFound
		}
		if err != nil {
			return SkillDTO{}, safe("get skill", err)
		}
		if previous.WorkspaceID != in.WorkspaceID {
			return SkillDTO{}, ErrSkillNotFound
		}
		item.CreatedAt, item.Revision = previous.CreatedAt, previous.Revision+1
	}
	if err := s.store.UpsertSkill(s.ctx, item); err != nil {
		return SkillDTO{}, safe("save skill", err)
	}
	return skillDTO(item), nil
}

func (s *Service) ListSkills(workspaceID string) ([]SkillDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if workspaceID == "" {
		return nil, ErrInvalidInput
	}
	items, err := s.store.ListSkills(s.ctx, workspaceID)
	if err != nil {
		return nil, safe("list skills", err)
	}
	result := make([]SkillDTO, 0, len(items))
	for _, item := range items {
		result = append(result, skillDTO(item))
	}
	return result, nil
}

func (s *Service) enabledSkillSnapshot(workspaceID string) (string, error) {
	items, err := s.store.ListSkills(s.ctx, workspaceID)
	if err != nil {
		return "", safe("list enabled skills", err)
	}
	var instructions strings.Builder
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		if instructions.Len()+len(item.Name)+len(item.Content)+64 > 64*1024 {
			return "", ErrInvalidInput
		}
		instructions.WriteString("\nSkill: ")
		instructions.WriteString(item.Name)
		instructions.WriteString("\n")
		instructions.WriteString(item.Content)
		instructions.WriteString("\n")
	}
	return instructions.String(), nil
}
