# Etapa Brainstorm — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a first-class SDD stage `brainstorm` between SPEC and Plan that shows the AI's application flow (Mermaid) and, when the solution has screens, its HTML design, approved before the Plan is generated.

**Architecture:** `brainstorm` joins `sdd.Stages` and the pipeline design workspace as a fourth document whose content is canonical JSON. A dedicated generation phase (own prompt, design guide, schema and limits) writes it; a new approval publishes Discovery/SPEC/Brainstorm and starts the Plan by itself. The frontend gets a single source of stage order/labels, a responsive pipeline bar and a Brainstorm screen (Mermaid view + sandboxed iframe design view).

**Tech Stack:** Go (Wails v3 backend, SQLite migrations), React + TypeScript + zod, vitest, Playwright, `mermaid` (new, MIT).

**Spec:** `docs/superpowers/specs/2026-10-17-brainstorm-stage-design.md`

## Global Constraints

- Stage order: Discovery → SPEC → Brainstorm → Plan → Code → QA → PRs; stage id `brainstorm`, label `Brainstorm`.
- Skipped reason for old pipelines (exact copy): `a etapa não existia quando este trabalho foi preparado`.
- Brainstorm phase limits: 32 768 output tokens, document ≤ 512 KB (constants of their own, not the generic design limits); at most 12 screens; `DESIGN.md` read up to 32 KB.
- HTML rules: complete self-contained document, CSS embedded; refuse `<script>`, `on*=` attributes, `javascript:`, `<iframe>`, `<object>`, `<embed>`, `<form action=` with external URL, any `http:`/`https:`/`//` URL; images and fonts only `data:`.
- Iframe: `sandbox=""` plus injected `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:">`.
- Mermaid loaded with `import('mermaid')` only on the Brainstorm screen, `securityLevel: 'strict'`; listed in `THIRD_PARTY_NOTICES.md`.
- Code context cap for the approved flow + screens: 200 KB; screens past it go by name and purpose only.
- UI copy in pt-BR with en/es translations; `node scripts/check-i18n.mjs` must report `problemas: 0`.
- Tests: Go with a resolved TMPDIR (`TMPDIR=$(cd "$TMPDIR" && pwd -P)`), `internal/application` with `-timeout 40m` under `-race`; Playwright compared against the 20 pre-existing failures on main.

## Review Focus

- A model answer cut mid-HTML (invalid JSON) must be refused and retried once with the reason, never saved half-written — Task 5 test `TestBrainstormTruncatedOutputIsRetriedNotSaved`.
- Design that pulls an external font or image (`<link href="https://…">`, `url(https://…)`) must be refused with a reason the AI can act on — Task 4 table rows.
- Mermaid labels with accents and quotes (pt-BR text) must render — Task 11 test `renders a flow with accented labels`.
- Approving while a generation is running, or while Brainstorm is stale, must be impossible — Task 6 test `TestApproveBrainstormRefusesStaleOrRunning`.
- A pipeline that sat in SPEC with documents already written when 059 runs must offer Brainstorm next (generation target `all` starts at `brainstorm`) — Task 3 test `TestStagesForAllAfterSpecStartsAtBrainstorm`.

---

### Task 1: The `brainstorm` stage in the flow

**Files:**
- Modify: `internal/sdd/flow.go`
- Test: `internal/sdd/flow_test.go`

**Interfaces:**
- Produces: `sdd.Brainstorm Stage = "brainstorm"`; `sdd.Stages = [...]Stage{Discovery, Spec, Brainstorm, Plan, Code, Eval, PRs}`.

- [ ] **Step 1: Write the failing test** `TestBrainstormSitsBetweenSpecAndPlan`: `NewFlow()` has `Status[Brainstorm] == Pending`; advancing from `Spec` with evidence makes `Current == Brainstorm`; advancing again makes `Current == Plan`; `Skip` from `Brainstorm` is allowed (returns `Plan`, status `Skipped`).
- [ ] **Step 2: Run** `TMPDIR=$(cd "$TMPDIR" && pwd -P) go test ./internal/sdd/ -run Brainstorm` — Expected: FAIL (undefined `Brainstorm`).
- [ ] **Step 3: Implement** the constant and the new order in `Stages`.
- [ ] **Step 4: Run** `go test ./internal/sdd/ ./internal/application/ -run 'Flow|Stage' -timeout 40m` — Expected: PASS; fix any test that hard-codes six stages to the new seven.
- [ ] **Step 5: Commit** `feat: brainstorm stage between SPEC and Plan`.

### Task 2: Migration 059 and the workspace's fourth document

**Files:**
- Create: `internal/storage/migrations/059_brainstorm_stage.sql`
- Modify: `internal/storage/sqlite/pipeline_design.go:22` (`pipelineDesignStages`), `:75` (`validPipelineDesignStage`), `:96` (`emptyPipelineDesign`); `internal/storage/sqlite/stage_executors.go:48`; `internal/application/stage_executors.go:34`
- Test: `internal/storage/sqlite/migration_brainstorm_test.go`

**Interfaces:**
- Consumes: `sdd.Brainstorm` (Task 1).
- Produces: `pipelineDesignStages = [...]sdd.Stage{sdd.Discovery, sdd.Spec, sdd.Brainstorm, sdd.Plan}`; tables accept `brainstorm` wherever they accept `spec`.

- [ ] **Step 1: Write the failing test** `TestMigration059AddsBrainstorm`: open a DB migrated to 058 with (a) a pipeline at `current_stage='code'`, (b) one at `'spec'` with a design workspace holding SPEC v1; migrate to latest; assert (a) `stage_status.brainstorm == "skipped"` and a `pipeline_transitions` row with reason `a etapa não existia quando este trabalho foi preparado`; (b) `stage_status.brainstorm == "pending"` and an empty `brainstorm` document; inserting a version with `stage='brainstorm'` and an executor row with `stage='brainstorm'` succeeds; all pre-existing rows survive.
- [ ] **Step 2: Run** `go test ./internal/storage/sqlite/ -run Migration059` — Expected: FAIL.
- [ ] **Step 3: Implement** 059 rebuilding the 042 tables (`pipeline_design_workspaces.phase`, `_versions.stage`, `_attempts.target/phase`, `_attempt_sessions.stage`, `_messages.target`) and `workspace_stage_executors` (050) with CHECKs that add `'brainstorm'`, recreating indexes and triggers (pattern: `044_…`, `053_…`), then the `stage_status` JSON backfill (`json_set`) and transitions; update the Go stage lists named above.
- [ ] **Step 4: Run** `go test ./internal/storage/... -run 'Migration|PipelineDesign|StageExecutor'` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: migration 059 adds the brainstorm stage to the design workspace`.

### Task 3: Dependencies, staleness and phase order

**Files:**
- Modify: `internal/catalog/pipeline_design.go` (`PipelineDesignSourceDigest`), `internal/storage/sqlite/pipeline_design.go` (`stalePipelineDesignDependents`, `validPipelineDesignGeneratedSet`, `BeginPipelineDesignAttempt` required stages, `SetPipelineDesignPhase`, `LinkPipelineDesignSession` filters), `internal/application/pipeline_design_model.go:30` (`pipelineDesignStagesFor`), `internal/application/pipeline_design_dto.go:125` (`designStages`), `internal/application/pipeline_design.go:27` (`designStage`), `internal/application/pipeline_design_generation.go:26` (selection count 3 → 4)
- Test: `internal/application/pipeline_design_brainstorm_test.go`, `internal/catalog/pipeline_design_test.go`

**Interfaces:**
- Produces: `pipelineDesignStagesFor(target, workspace)` → `all` = `[spec, brainstorm]` when both empty, `[brainstorm]` when SPEC exists and Brainstorm empty, `[discovery, spec, brainstorm]` otherwise; `spec` → `[spec, brainstorm]`; `brainstorm` → `[brainstorm]`; `plan` → `[plan]` (Plan is never part of `all` anymore). Source digests: Brainstorm = D+S, Plan = D+S+B.

- [ ] **Step 1: Write the failing tests** `TestStagesForAllAfterSpecStartsAtBrainstorm` (table over the targets above), `TestEditingSpecMakesBrainstormAndPlanStale`, `TestEditingBrainstormMakesPlanStale`, `TestPlanSourceDigestIncludesBrainstorm`.
- [ ] **Step 2: Run** `go test ./internal/catalog/ ./internal/application/ -run 'StagesFor|Stale|SourceDigest' -timeout 40m` — Expected: FAIL.
- [ ] **Step 3: Implement** the lists and cascades named in **Files**.
- [ ] **Step 4: Run** the same command plus `-run PipelineDesign` — Expected: PASS (update existing design tests that assumed `all` = SPEC + Plan).
- [ ] **Step 5: Commit** `feat: brainstorm depends on SPEC and feeds Plan`.

### Task 4: Brainstorm content and HTML safety

**Files:**
- Create: `internal/application/brainstorm_content.go`
- Test: `internal/application/brainstorm_content_test.go`

**Interfaces:**
- Produces:
  - `type BrainstormScreen struct { Name, Purpose, HTML string }` (json `name`, `purpose`, `html`)
  - `type BrainstormDesign struct { Screens []BrainstormScreen }` (json `screens`)
  - `type BrainstormContent struct { Flow string; Design *BrainstormDesign; NoDesignReason string; Notes string }` (json `flow`, `design`, `noDesignReason`, `notes`)
  - `func parseBrainstormOutput(raw string) (BrainstormContent, string, error)` — decodes `{"brainstorm": BrainstormContent, "reply": string}` strictly, validates, returns content and reply; errors wrap `errInvalidSDDOutput` with a reason string usable in the retry prompt.
  - `func validateBrainstormHTML(html string) error`
  - `func (c BrainstormContent) Canonical() string` — stable JSON stored as the document content.
  - `const MaxBrainstormDocumentBytes = 512 * 1024`, `const maxBrainstormScreens = 12`.

- [ ] **Step 1: Write the failing tests** `TestParseBrainstormOutput` (valid with design; valid with `design: null` and non-empty `noDesignReason`; empty `flow` refused; `design: null` with empty reason refused; 13 screens refused; unknown key refused) and `TestValidateBrainstormHTML` table, one row each refused: `<script>`, `onclick=`, `javascript:`, `<iframe`, `<object`, `<embed`, `<form action="https://x">`, `<link href="https://fonts…">`, `url(https://x/y.png)`, `src="//x"`; accepted: inline `<style>`, `src="data:image/png;base64,…"`.
- [ ] **Step 2: Run** `go test ./internal/application/ -run 'Brainstorm(Output|HTML)' -timeout 40m` — Expected: FAIL.
- [ ] **Step 3: Implement** with a case-insensitive scan (lowercased copy, regexes for `on[a-z]+\s*=`, `(https?:)?//` inside `href|src|action|url(`); size checks against `MaxBrainstormDocumentBytes`.
- [ ] **Step 4: Run** the same — Expected: PASS.
- [ ] **Step 5: Commit** `feat: brainstorm content model with HTML safety checks`.

### Task 5: The Brainstorm generation phase

**Files:**
- Create: `internal/application/brainstorm_prompt.go` (system prompt, Harflex design guide, schema, project design context)
- Modify: `internal/application/pipeline_design_generation.go` (`runPipelineDesignPhase` / phase loop branch for `sdd.Brainstorm`), `internal/application/pipeline_design_model.go` (selection limits for this phase)
- Test: `internal/application/brainstorm_generation_test.go`

**Interfaces:**
- Consumes: `parseBrainstormOutput`, `BrainstormContent.Canonical` (Task 4); `pipelineDesignStagesFor` (Task 3).
- Produces: `const brainstormOutputTokens = 32768`; `var brainstormOutputSchema json.RawMessage`; `func projectDesignContext(projectPath string) string` (DESIGN.md ≤ 32 KB, empty when absent); `var ErrBrainstormModelTooSmall = errors.New("the model chosen for Brainstorm cannot write a document of this size")`.

- [ ] **Step 1: Write the failing tests** (stub CLI document generator as in `project_memory_test.go`): `TestBrainstormPhaseWritesCanonicalContent` (target `all` from Discovery → SPEC then Brainstorm generated, Plan untouched, prompt contains the design guide marker, the SPEC text and the project's DESIGN.md text); `TestBrainstormTruncatedOutputIsRetriedNotSaved` (first answer invalid JSON, second valid → one retry whose prompt contains the reason; document saved from the second); `TestBrainstormRefusesSmallModel` (selection `MaxOutputTokens: 4096` → `ErrBrainstormModelTooSmall`, `ErrorCode(err) == "brainstorm_model_too_small"`, no session created); `TestBrainstormSecondInvalidAnswerPausesAttempt` (two invalid answers → attempt `failed` with code `invalid_output`, documents untouched).
- [ ] **Step 2: Run** `go test ./internal/application/ -run 'BrainstormPhase|BrainstormTruncated|BrainstormRefuses' -timeout 40m` — Expected: FAIL.
- [ ] **Step 3: Implement** the prompt (pt-BR; decision rule for design from the spec; guide topics: hierarchy and spacing, empty/loading/error states, real copy, accessibility with 44 px touch targets, responsiveness, no frame-in-frame, follow the project's identity), the phase branch (schema, limits, one corrective retry, `Canonical()` as document content) the capacity check before creating the session, and the `brainstorm_model_too_small` code in `ErrorCode`.
- [ ] **Step 4: Run** the same — Expected: PASS.
- [ ] **Step 5: Commit** `feat: brainstorm generation phase with the Harflex design guide`.

### Task 6: Two approvals

**Files:**
- Modify: `internal/storage/sqlite/pipeline_design.go:1062` (`ApprovePipelineDesign`), add `ApprovePipelineBrainstorm`; `internal/catalog/pipeline_design.go` (request type); `internal/application/pipeline_design.go:233` and a new `ApprovePipelineBrainstorm`; `internal/application/pipeline_design.go:141` (continuation inherits Brainstorm)
- Test: `internal/application/pipeline_design_brainstorm_approval_test.go`

**Interfaces:**
- Produces:
  - Store: `func (s *Store) ApprovePipelineBrainstorm(ctx context.Context, in catalog.PipelineDesignApproveRequest) (catalog.PipelineRun, error)` — digests for `discovery`, `spec`, `brainstorm`; publishes the three to `pipeline_artifacts`; statuses D/S/B `completed`, `plan` `active`; CAS `current_stage IN ('discovery','spec','brainstorm')` → `plan`.
  - Service: `func (s *Service) ApprovePipelineBrainstorm(in ApprovePipelineDesignInput) (PipelineDTO, error)` — after the store call, starts `PreparePipelineDesign` with target `plan` and message `Prepare o Plan a partir da SPEC e do Brainstorm aprovados.` in the background.
  - `ApprovePipelineDesign` requires the four digests and `current_stage = 'plan'`.

- [ ] **Step 1: Write the failing tests** `TestApproveBrainstormPublishesAndStartsPlan`, `TestApproveBrainstormRefusesStaleOrRunning` (stale Brainstorm → error, active attempt → error, wrong digest → error), `TestApprovePlanNeedsBrainstormDigest`, `TestContinuationInheritsBrainstorm`.
- [ ] **Step 2: Run** `go test ./internal/application/ ./internal/storage/sqlite/ -run 'Approve|Continuation' -timeout 40m` — Expected: FAIL.
- [ ] **Step 3: Implement** both store and service paths.
- [ ] **Step 4: Run** the same — Expected: PASS.
- [ ] **Step 5: Commit** `feat: approve brainstorm before the plan`.

### Task 7: Code and QA receive the approved flow and screens

**Files:**
- Modify: `internal/application/sdd_execution.go:227-239` (context documents for Code/Eval prompts)
- Test: `internal/application/sdd_execution_brainstorm_test.go`

**Interfaces:**
- Consumes: `BrainstormContent` (Task 4); artifacts published in Task 6.
- Produces: `func brainstormExecutionContext(content string, budget int) string` — flow + screens HTML up to `budget` (200 KB), then remaining screens as `- <name>: <purpose>`.

- [ ] **Step 1: Write the failing tests** `TestCoderPromptCarriesApprovedFlowAndScreens` and `TestBrainstormContextRespectsBudget` (3 screens of 90 KB → first two in full, third by name and purpose); QA prompt contains the flow but no HTML.
- [ ] **Step 2: Run** `go test ./internal/application/ -run 'CoderPromptCarries|BrainstormContext' -timeout 40m` — Expected: FAIL.
- [ ] **Step 3: Implement** and wire it into the Code and Eval prompts.
- [ ] **Step 4: Run** the same — Expected: PASS.
- [ ] **Step 5: Commit** `feat: code and QA use the approved brainstorm`.

### Task 8: One source for stages in the frontend

**Files:**
- Modify: `frontend/src/lib/pipelineStages.ts` (single source), `frontend/src/lib/backend.ts:44` (`pipelineStage` enum), `frontend/src/lib/pipelineDesign.ts:4,12` (4 documents; brainstorm content schema), and replace the duplicated maps in `components/PipelineBar.tsx`, `features/pipelines/PipelineDesignDocument.tsx`, `PipelinesPage.tsx`, `RecentWork.tsx`, `StageActivityPane.tsx`, `PipelineExecutionReview.tsx`, `PipelinePhaseExecutors.tsx`, `designQuestionParser.ts`; fixtures `test/pipelineDesignFixture.ts`, `test/fakeBackend.ts`, `test/fixture.tsx`
- Test: `frontend/src/lib/pipelineStages.test.ts`

**Interfaces:**
- Produces (in `lib/pipelineStages.ts`): `export const stageOrder: readonly PipelineStage[]`; `export const designStageOrder = ['discovery','spec','brainstorm','plan'] as const`; `export function stageLabel(stage: PipelineStage): string`; `export const stageGroups: { id: 'prepare'|'execute'|'deliver'; label: string; stages: PipelineStage[] }[]` (Preparação: discovery, spec, brainstorm, plan · Execução: code, eval · Entrega: prs); in `lib/pipelineDesign.ts`: `brainstormContentSchema` and `type BrainstormContent`.

- [ ] **Step 1: Write the failing test** asserting the order, the groups and that `pipelineDesignSchema` parses a workspace with four documents and rejects three; plus `PipelinePhaseExecutors.test.tsx` case: the per-phase model card lists a `Brainstorm` row between SPEC and Plan.
- [ ] **Step 2: Run** `cd frontend && npx vitest run src/lib/pipelineStages.test.ts` — Expected: FAIL.
- [ ] **Step 3: Implement** and switch every listed file to the single source.
- [ ] **Step 4: Run** `npx tsc --noEmit && npx vitest run` — Expected: PASS.
- [ ] **Step 5: Commit** `refactor: one source for pipeline stages, with brainstorm`.

### Task 9: Responsive pipeline bar

**Files:**
- Modify: `frontend/src/components/PipelineBar.tsx`, `frontend/src/components/pipelineBar.css`
- Test: `frontend/src/components/PipelineBar.test.tsx`

**Interfaces:**
- Consumes: `stageOrder`, `stageGroups`, `stageLabel` (Task 8).
- Produces: markup with `.pipeline-line` (wide: seven nodes with names + `.pipeline-now` card "<etapa> · <status> · depois: <próximo passo>") and `.pipeline-groups` (narrow: grouped markers, current named); a container query on `.pipeline` switches them at 880 px of available width; skipped stages carry the reason in `title` and `aria-label`.

- [ ] **Step 1: Write the failing tests** `shows every stage name and the now card` (both renders present in the DOM, current marked `aria-current="step"`), `names markers for screen readers in the grouped form`, `explains a skipped stage`.
- [ ] **Step 2: Run** `npx vitest run src/components/PipelineBar.test.tsx` — Expected: FAIL.
- [ ] **Step 3: Implement** both forms (CSS `@container` decides which is visible; keep the existing click-to-open-stage behavior).
- [ ] **Step 4: Run** the same — Expected: PASS.
- [ ] **Step 5: Commit** `feat: responsive pipeline bar for seven stages`.

### Task 10: Backend bindings for the new approval

**Files:**
- Modify: `frontend/src/lib/backend.ts` (`Backend.approvePipelineBrainstorm(input: ApprovePipelineDesignInput): Promise<Pipeline>`), `frontend/src/lib/wailsBackend.ts`, `frontend/src/test/fakeBackend.ts`, `frontend/src/test/pipelineDesignFixture.ts`; regenerate `frontend/bindings` with `make bindings`
- Test: `frontend/src/lib/backend.test.ts`

- [ ] **Step 1: Write the failing test** that the fake backend's `approvePipelineBrainstorm` moves the fixture pipeline to `plan` with D/S/B completed, and that `errorMessage({ cause: { code: 'brainstorm_model_too_small' } })` reads `O modelo escolhido para o Brainstorm não comporta um documento deste tamanho. Escolha outro modelo para a fase.`
- [ ] **Step 2: Run** `npx vitest run src/lib/backend.test.ts` — Expected: FAIL.
- [ ] **Step 3: Implement** the interface method, the Wails call and the fixtures.
- [ ] **Step 4: Run** `make bindings && cd frontend && npx tsc --noEmit && npx vitest run src/lib` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: frontend binding for brainstorm approval`.

### Task 11: The Brainstorm screen

**Files:**
- Create: `frontend/src/features/pipelines/brainstorm/BrainstormStage.tsx`, `FlowView.tsx`, `DesignView.tsx`, `brainstorm.css`; `frontend/src/i18n/dictionaries/brainstorm.ts`
- Modify: `frontend/package.json` (`mermaid`), `THIRD_PARTY_NOTICES.md`, `frontend/src/i18n/dictionaries/index.ts`
- Test: `frontend/src/features/pipelines/brainstorm/BrainstormStage.test.tsx`, `DesignView.test.tsx`, `FlowView.test.tsx`

**Interfaces:**
- Consumes: `BrainstormContent` (Task 8), `approvePipelineBrainstorm` (Task 10), the design studio composer (`PipelineDesignStudio` conversation with target `brainstorm`).
- Produces: `export function BrainstormStage(props: { backend: Backend; pipeline: Pipeline; design: PipelineDesign; onPipelineChange(p: Pipeline): void })`; `export function FlowView(props: { source: string; onAskFix(error: string): void })`; `export function DesignView(props: { screens: NonNullable<BrainstormContent['design']>['screens'] })`; `export function sandboxedDocument(html: string): string` (injects the CSP meta from Global Constraints).

- [ ] **Step 1: Write the failing tests**: layout B with design (design region right, flow + conversation left, approve bar label `Aprovar fluxo e design`); without design (no design region, `noDesignReason` shown, label `Aprovar fluxo`); `renders a flow with accented labels` (`Cliente pede a baixa → "Contrato venceu?"`); invalid Mermaid shows the parser error and `Pedir correção à IA` calls `onAskFix` with it; stale shows `Atualizar o Brainstorm` and disables approval; `DesignView` iframe has `sandbox=""` and its `srcdoc` contains the CSP meta; Desktop/Mobile toggle changes the frame width; screen tabs switch screens; while generating, both boxes show the run progress and the flow appears as soon as the document has it; a `skipped` Brainstorm shows the reason and no generate or approve action; a `brainstorm_model_too_small` failure shows its message with a `Trocar o modelo da fase` button that opens the per-phase model card.
- [ ] **Step 2: Run** `npx vitest run src/features/pipelines/brainstorm` — Expected: FAIL.
- [ ] **Step 3: Implement** (mermaid via `import('mermaid')` + `mermaid.initialize({ startOnLoad: false, securityLevel: 'strict' })`, render to SVG into the flow box; zoom and "Ampliar" in a dialog with drag; "Ver código"; "Editar" reusing the document editor for the JSON fields; copy translated in `brainstorm.ts`).
- [ ] **Step 4: Run** `npx tsc --noEmit && npx vitest run && node scripts/check-i18n.mjs` — Expected: PASS and `problemas: 0`.
- [ ] **Step 5: Commit** `feat: brainstorm screen with rendered flow and sandboxed design`.

### Task 12: Wiring, end-to-end and docs

**Files:**
- Modify: `frontend/src/features/pipelines/PipelinesPage.tsx` (route the `brainstorm` stage to `BrainstormStage`), `PipelineDesignStudio.tsx` (composer target list from `designStageOrder`, texts "Preparar SPEC e Brainstorm", stale target), `frontend/src/test/fixture.tsx` + `frontend/src/test/showcaseFixture.ts` (Brainstorm content with two screens), `README.md`, `DESIGN.md`
- Create: `frontend/e2e/brainstorm.spec.ts`

- [ ] **Step 1: Write the failing E2E** `brainstorm.spec.ts`: prepare from Discovery → SPEC and Brainstorm appear, Plan does not; the Brainstorm screen shows the flow and two design screens; approve → Plan generated; approve Plan → Code active; a no-design fixture hides the design box; width 320 has no horizontal scroll.
- [ ] **Step 2: Run** `cd frontend && npx playwright test e2e/brainstorm.spec.ts` — Expected: FAIL.
- [ ] **Step 3: Implement** the wiring and fixtures; README "What it looks like" gets the Brainstorm step; DESIGN.md documents the bar's two forms and the Brainstorm layout.
- [ ] **Step 4: Run** the full suites: `npx vitest run`, `npx playwright test` (only the 20 baseline failures), `TMPDIR=$(cd "$TMPDIR" && pwd -P) go test -race -timeout 40m ./internal/...`, `make screenshots` — Expected: all green except the baseline.
- [ ] **Step 5: Commit** `feat: brainstorm stage end to end`.
