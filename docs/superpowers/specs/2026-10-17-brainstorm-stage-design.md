# Etapa Brainstorm no pipeline SDD — design

Data: 2026-10-17 · Status: aprovado em conversa, aguardando revisão desta spec

## Objetivo

Depois da SPEC, a pessoa vê **como a solução vai funcionar** antes de existir um Plan: o **fluxo da aplicação** (Mermaid,
renderizado) e, quando a solução tem telas, o **design proposto** (HTML renderizado). Ela aprova o fluxo e o design, e só
então o Plan é gerado em cima deles. O Code recebe o fluxo e as telas aprovadas como referência do que construir.

Sucesso:
- O Brainstorm é uma etapa de verdade, visível na barra do pipeline, entre SPEC e Plan.
- O fluxo sempre existe; o design aparece só quando a IA conclui, a partir da SPEC, que a solução tem algo que alguém vê
  e usa. Sem design, o quadro de design não aparece.
- O design segue a identidade visual do projeto quando ela existe.
- A barra do pipeline com 7 etapas não fica espremida.

Fora do escopo: editor visual de telas, protótipo navegável com scripts, export do design para Figma.

## Decisões tomadas com o usuário

| Tema | Decisão |
|---|---|
| Posição e natureza | Etapa de primeira classe em `sdd.Stages` (abordagem B): Discovery → SPEC → **Brainstorm** → Plan → Code → QA → PRs |
| Aprovação | Portão antes do Plan: aprova-se fluxo + design; o Plan é gerado em seguida, automaticamente |
| Design opcional | A IA decide pela SPEC; sem telas, design vazio com uma frase do porquê, e o quadro some |
| Estilo | Segue a identidade do projeto (`DESIGN.md`, tokens, memória do projeto) quando existir |
| Skill de design | Guia próprio do Harflex embutido no prompt da fase (inspirado em boas práticas tipo impeccable/superpowers, sem copiar conteúdo) |
| Barra do pipeline | Responsiva: linha de progresso com cartão da etapa atual quando há espaço; etapas agrupadas (Preparação · Execução · Entrega) quando aperta |
| Tela do Brainstorm | Layout B: design em destaque à direita; fluxo e conversa numa coluna à esquerda; barra de aprovação fixa embaixo |

## 1. Etapas e fluxo do trabalho

- `sdd.Stages` = `{Discovery, Spec, Brainstorm, Plan, Code, Eval, PRs}` (`internal/sdd/flow.go`), com `Brainstorm Stage = "brainstorm"`.
- A preparação passa a ter **duas aprovações**:
  1. **Aprovar fluxo e design**: a IA gera SPEC e Brainstorm e para. Ao aprovar, Discovery, SPEC e Brainstorm ficam
     `completed`, `current_stage` vira `plan` e a geração do Plan começa sozinha (mesma tentativa/conversa de preparação).
  2. **Aprovar o Plan**: como hoje — publica e avança para `code`.
- Ajustes: conversa com a IA (alvo "Brainstorm" no seletor do composer) ou edição direta, como SPEC e Plan.
- Desatualização: editar Discovery → SPEC, Brainstorm e Plan desatualizados; editar SPEC → Brainstorm e Plan; editar
  Brainstorm → Plan. Um documento desatualizado não pode ser aprovado; a tela oferece "Atualizar o Brainstorm".
- Trabalhos existentes:
  - `current_stage` em `plan`, `code`, `eval`, `prs` ou concluídos: Brainstorm = `skipped`, motivo "a etapa não existia
    quando este trabalho foi preparado" (registrado em `pipeline_transitions`). O andamento não muda.
  - `current_stage` em `discovery` ou `spec`: Brainstorm = `pending`; passam pela etapa normalmente.
- Modelo por fase: o Brainstorm ganha linha própria no cartão "Modelo por fase" (`workspace_stage_executors`), com
  fallback para o padrão das Configurações quando não há linha.

## 2. Conteúdo e geração

Documento do Brainstorm (quarto documento do espaço de preparação, JSON canônico, com versões, digest e histórico):

```json
{
  "flow": "flowchart TD\n  A[Cliente pede a baixa] --> B{Contrato venceu?} ...",
  "design": {
    "screens": [
      { "name": "Lista de contratos", "purpose": "…", "html": "<!doctype html><html>…</html>" }
    ]
  },
  "noDesignReason": "",
  "notes": "## Decisões\n…\n## Perguntas em aberto\n…"
}
```

- `design` é `null` quando não há telas; então `noDesignReason` traz uma frase ("só backend: sem telas novas").
- Cada tela: HTML completo e autocontido, CSS embutido, **sem** `<script>`, atributos `on*`, `javascript:`, `<iframe>`,
  `<object>`/`<embed>`, formulários com `action` externo nem URLs externas (`http(s):`, `//`). Imagens só `data:`.
- Validação no backend (`parseBrainstormOutput`): decoder estrito (sem chaves extras), Mermaid não vazio, até 12 telas,
  HTML validado pelas regras acima. Resposta inválida é recusada e a IA recebe o motivo para corrigir (uma nova tentativa
  na mesma conversa, como já acontece com saídas inválidas).
- Prompt da fase (`brainstormSystemPrompt` + `pipelineDesignPrompt` com `outputDocument: "brainstorm"`):
  - entradas: Discovery, SPEC, memória do projeto, `DESIGN.md` da raiz do projeto quando existir (até 32 KB) e, se
    houver, arquivos de tokens declarados nele;
  - **guia de design do Harflex** (texto nosso): hierarquia e espaçamento, estados vazio/carregando/erro, textos reais,
    acessibilidade (contraste, foco, alvos de 44 px no toque), responsividade, nada de moldura dentro de moldura,
    consistência com a identidade do projeto;
  - regra de decisão do design: há design quando a SPEC cria ou muda algo que uma pessoa vê ou usa (telas, formulários,
    painéis, e-mails); não há quando é só backend, integração, job ou dados.
- Limites próprios: saída de até 32 768 tokens e documento de até 512 KB (as outras fases seguem 4 096 tokens / 64 KB).
  Antes de gerar, se o `maxOutputTokens` ou o contexto do modelo escolhido não comportarem, a geração não começa e a tela
  explica e oferece trocar o modelo da fase.
- Mermaid inválido: validado no frontend (`mermaid.parse`); o quadro mostra o erro e "Pedir correção à IA", que envia o
  erro como pedido de ajuste com alvo Brainstorm.

## 3. Layout e UX

### Barra do pipeline (responsiva, `PipelineBar`)
- Container query sobre a largura disponível (não a janela), funcionando com o painel lateral aberto.
- **Larga**: linha contínua com as 7 etapas (nome curto sob cada nó, concluídas com check, atual destacada) e um cartão à
  direita: etapa atual, status e próximo passo ("Plan · gerando · depois: aprovar e ir para o Code").
- **Estreita**: grupos rotulados Preparação (Discovery, SPEC, Brainstorm, Plan), Execução (Code, QA), Entrega (PRs); só a
  etapa atual mostra nome e status; as demais são marcadores com nome em `title` e `aria-label`.
- Etapa pulada: apagada, motivo no hover. Clicar numa etapa abre a tela dela (comportamento atual).

### Tela do Brainstorm (layout B)
- **Direita (destaque) — Design**: abas por tela, alternância Desktop/Mobile (largura do quadro), "Abrir em tamanho
  cheio". Renderização em `<iframe sandbox="" srcdoc=…>` com `<meta http-equiv="Content-Security-Policy"
  content="default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:">` injetado.
- **Esquerda — Fluxo**: Mermaid renderizado (zoom, "Ampliar" com arrastar), botão "Ver código". Abaixo, a **conversa**
  de ajustes (mesmo composer do Preparar trabalho, alvo Brainstorm).
- **Sem design**: o quadro some; fluxo e conversa ocupam a área; aviso curto com `noDesignReason`.
- **Barra fixa inferior**: versão e modelo; **Editar** (código Mermaid e HTML de cada tela); **Pedir ajuste**;
  **Aprovar fluxo e design** ("Aprovar fluxo" sem design).
- Gerando: indicador de progresso da execução nos quadros; o fluxo aparece assim que fica pronto.
- Desatualizado: faixa no topo com "Atualizar o Brainstorm"; aprovação bloqueada até atualizar.

## 4. Dados, migração, aprovação e Code/QA

### Backend
- `internal/sdd/flow.go`: novo estágio; `Skip` continua proibido para Code/Eval; Brainstorm pode ser pulado só pela
  migração de trabalhos antigos (não há botão de pular na UI nesta versão).
- Espaço de preparação: `brainstorm` entra nas listas de estágios de design (`designStages`, `designStage`,
  `pipelineDesignStages`, `validPipelineDesignStage`), em `pipelineDesignStagesFor` (alvo `all` = SPEC → Brainstorm e
  para), em `PipelineDesignSourceDigest` (Brainstorm depende de D+S; Plan de D+S+B), `stalePipelineDesignDependents`,
  `validPipelineDesignGeneratedSet`, `BeginPipelineDesignAttempt`, `SetPipelineDesignPhase`, `LinkPipelineDesignSession`
  e no limite de seleções (`pipelineDesignIntent`, 3 → 4).
- Nova operação `ApprovePipelineBrainstorm` (digests D, S, B): valida versões, digests, `SourceDigest` e procedência;
  publica D, S, B em `pipeline_artifacts`; marca os três `completed`, `current_stage = plan` (CAS em `discovery|spec|
  brainstorm`), registra a transição e inicia a geração do Plan.
- `ApprovePipelineDesign` passa a exigir os digests de D, S, B e P e a aceitar `current_stage = plan`.
- Continuação (`deriveAuthoringPipeline`) herda SPEC, Brainstorm e Plan.
- Code: o prompt do Coder recebe o fluxo e as telas aprovadas (`sdd_execution.go`, contexto ao lado de SPEC/Plan), com
  teto de 200 KB; telas além do teto entram por nome e propósito. QA recebe o fluxo.

### Migração 059
- Recria as tabelas da 042 (`pipeline_design_workspaces.phase`, `_versions.stage`, `_attempts.target/phase`,
  `_attempt_sessions.stage`, `_messages.target`) e a `workspace_stage_executors` da 050 com os CHECKs aceitando
  `brainstorm`, preservando dados, índices e triggers (padrão das migrações 044 e 053).
- Backfill de `pipeline_runs.stage_status` (JSON): `brainstorm` = `skipped` para trabalhos além da SPEC (com linha em
  `pipeline_transitions` e o motivo) e `pending` para os demais. Documentos de preparação ganham a linha vazia do
  Brainstorm.

### Frontend
- Zod: `pipelineStage` com `brainstorm`; o espaço de preparação passa a exigir 4 documentos; novo esquema do conteúdo
  do Brainstorm.
- Fonte única de ordem e rótulos de etapas (hoje duplicados em `PipelineBar`, `designStageOrder`, `PipelinesPage`,
  `RecentWork`, `StageActivityPane`, `PipelineExecutionReview`, `PipelinePhaseExecutors`, `designQuestionParser`,
  `lib/pipelineStages.ts`).
- Mermaid (MIT) como dependência, carregado por `import()` só na tela do Brainstorm, `securityLevel: 'strict'`;
  registrado em `THIRD_PARTY_NOTICES.md`.
- Textos novos em pt-BR com traduções en/es (verificador `check-i18n` sem problemas).

## 5. Erros e casos-limite
- Modelo sem capacidade para o limite: não gera; explica e oferece trocar o modelo da fase.
- Saída inválida (esquema, HTML perigoso, Mermaid vazio): recusada, IA pede correção; se falhar de novo, a tentativa
  pausa com o motivo e "Tentar novamente".
- Mermaid que não renderiza no frontend: erro no quadro + "Pedir correção à IA".
- Cancelamento durante a geração: igual às outras fases (documentos protegidos).
- Trabalho com Brainstorm `skipped`: a tela mostra o motivo e nenhum botão de gerar.

## 6. Testes
- Go: ordem e transições das etapas; aprovação em dois passos; desatualização em cascata; parse e validação do
  conteúdo (HTML perigoso recusado em cada regra); checagem de capacidade do modelo; migração 059 com banco antigo
  (trabalho além da SPEC → skipped; em SPEC → pending; dados preservados); contexto do Coder com teto.
- Frontend: tela do Brainstorm com design, sem design, Mermaid inválido, desatualizado e gerando; iframe com `sandbox` e
  CSP; barra do pipeline nas formas larga e estreita; aprovação chamando a operação certa.
- E2E: cenário completo no fixture (gerar → aprovar Brainstorm → Plan gerado → aprovar → Code), comparado à linha de
  base de 20 falhas pré-existentes; capturas do README refeitas (`make screenshots`).
