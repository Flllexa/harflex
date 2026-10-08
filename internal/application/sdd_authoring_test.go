package application

import (
	"errors"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/sdd"
)

func TestAuthoringPipelineRequiresOnlyUserDiscoveryAndBlocksLegacyAdvance(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, discovery := range []string{"", " \n ", strings.Repeat("x", 1024*1024+1)} {
		if _, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "invalid-discovery-0001", Discovery: discovery}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid Discovery accepted: %v", err)
		}
	}
	text := "# Exportar faturas\n\nObjetivo: entregar CSV local.\nRisco: dados pessoais.\nCritério: arquivo válido."
	created, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "authoring-create-0001", Discovery: text})
	if err != nil || created.Kind != "ai_authoring" || created.CurrentStage != "discovery" || created.Artifacts["discovery"].Content != text || created.Artifacts["discovery"].Author != "user" || created.Title != "Exportar faturas" {
		t.Fatal(created, err)
	}
	if _, err := s.AdvancePipeline(created.ID); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("authoring pipeline advanced without brainstorming gate: %v", err)
	}
	if _, err := s.SkipPipelineStage(SkipPipelineStageInput{PipelineID: created.ID, Reason: "pular"}); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("required Discovery skipped: %v", err)
	}
	if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: created.ID, Stage: "spec", Content: "SPEC manual"}); err == nil {
		t.Fatal("manual SPEC accepted in AI-authored pipeline")
	}
	legacy, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Legado", Objective: "Manter leitura"})
	if err != nil || legacy.Kind != "legacy" {
		t.Fatal(legacy, err)
	}
	if _, err := db.GetPipeline(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFrozenAuthoringDiscoveryRequiresExplicitDerivation(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "frozen-parent-0001", Discovery: "Discovery original integral"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeriveAuthoringPipeline(DeriveAuthoringPipelineInput{ParentPipelineID: parent.ID, RequestID: "unfrozen-child-0001", ExpectedRevision: parent.Revision, Discovery: "Versão revisada"}); err == nil {
		t.Fatal("unfrozen Discovery derived instead of edited")
	}
	if err := db.FreezeDiscovery(t.Context(), parent.ID, parent.Revision, parent.Artifacts["discovery"].Version); err != nil {
		t.Fatal(err)
	}
	parent, err = s.GetPipeline(parent.ID)
	if err != nil || parent.DiscoveryFrozenVersion != 1 {
		t.Fatal(parent, err)
	}
	if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: parent.ID, Stage: "discovery", Content: "Sobrescrever"}); err == nil {
		t.Fatal("approved Discovery changed in place")
	}
	child, err := s.DeriveAuthoringPipeline(DeriveAuthoringPipelineInput{ParentPipelineID: parent.ID, RequestID: "frozen-child-0001", ExpectedRevision: parent.Revision, Discovery: "# Versão revisada\n\nNovo contexto"})
	if err != nil || child.DerivedFromPipelineID != parent.ID || child.Artifacts["discovery"].Version != 1 || child.Artifacts["discovery"].Author != "user" || child.DiscoveryFrozenVersion != 0 {
		t.Fatal(child, err)
	}
	unchanged, err := s.GetPipeline(parent.ID)
	if err != nil || unchanged.Artifacts["discovery"].Content != "Discovery original integral" || unchanged.DiscoveryFrozenVersion != 1 {
		t.Fatal(unchanged, err)
	}
}

func TestLegacyPipelineCanCreateLinkedRecoveryPipelineFromDiscovery(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "TODO legado", Objective: "Objetivo original"})
	if err != nil {
		t.Fatal(err)
	}
	const discovery = "Criar um TODO com filtros e persistência local."
	parent, err = s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: parent.ID, Stage: "discovery", Content: discovery})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.DeriveAuthoringPipeline(DeriveAuthoringPipelineInput{ParentPipelineID: parent.ID, RequestID: "legacy-recovery-0001", ExpectedRevision: parent.Revision, Discovery: discovery})
	if err != nil || child.Kind != "ai_authoring" || child.DerivedFromPipelineID != parent.ID || child.CurrentStage != "discovery" || child.Artifacts["discovery"].Content != discovery {
		t.Fatalf("legacy recovery did not create a linked fresh SDD: %+v %v", child, err)
	}
}

func TestAuthoringDiscoveryRevisionRejectsStaleEditorAndUpdatesSummary(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	initial, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "revision-root-0001", Discovery: "# Primeiro título\n\nCritério inicial"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: initial.ID, Stage: "discovery", Content: "Sobrescrita sem revisão"}); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("legacy editor changed AI Discovery: %v", err)
	}
	revised, err := s.ReviseAuthoringDiscovery(ReviseAuthoringDiscoveryInput{PipelineID: initial.ID, ExpectedRevision: initial.Revision,
		ExpectedVersion: initial.Artifacts["discovery"].Version, Discovery: "# Segundo título\n\nCritério revisado"})
	if err != nil || revised.Title != "Segundo título" || revised.Objective == initial.Objective || revised.Revision != initial.Revision+1 ||
		revised.Artifacts["discovery"].Version != 2 || revised.Artifacts["discovery"].Author != "user" {
		t.Fatal(revised, err)
	}
	if _, err := s.ReviseAuthoringDiscovery(ReviseAuthoringDiscoveryInput{PipelineID: initial.ID, ExpectedRevision: initial.Revision,
		ExpectedVersion: initial.Artifacts["discovery"].Version, Discovery: "# Editor antigo"}); err == nil {
		t.Fatal("stale editor overwrote reviewed Discovery")
	}
	unchanged, err := s.GetPipeline(initial.ID)
	if err != nil || unchanged.Artifacts["discovery"].Content != "# Segundo título\n\nCritério revisado" || unchanged.Title != revised.Title {
		t.Fatal(unchanged, err)
	}
	if err := db.FreezeDiscovery(t.Context(), revised.ID, revised.Revision, revised.Artifacts["discovery"].Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviseAuthoringDiscovery(ReviseAuthoringDiscoveryInput{PipelineID: revised.ID, ExpectedRevision: revised.Revision + 1,
		ExpectedVersion: revised.Artifacts["discovery"].Version, Discovery: "# Edição depois do gate"}); err == nil {
		t.Fatal("frozen Discovery accepted an in-place revision")
	}
}
