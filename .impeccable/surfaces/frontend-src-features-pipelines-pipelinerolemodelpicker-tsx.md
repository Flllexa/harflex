---
version: 1
slug: "frontend-src-features-pipelines-pipelinerolemodelpicker-tsx"
primary_target: "frontend/src/features/pipelines/PipelineRoleModelPicker.tsx"
related_targets: ["frontend/src/features/pipelines/PipelinesPage.tsx", "frontend/src/features/workbench/Workbench.tsx"]
---

# Code/Eval — seleção de modelo por etapa

## THESIS
Escolher executor e modelo de Code ou Eval e confirmar a seleção antes de iniciar a sessão.

## OWN-WORLD
Ion Mint existente. Os seletores ficam dentro do controle do papel e usam os catálogos, bordas, tipografia, foco e estados já aprovados.

## STORY
Padrão global confirmado → catálogo completo do executor escolhido → modelo efetivo visível → override local opcional → iniciar o papel e guardar a seleção na sessão. A etapa não altera Settings.

## FIRST VIEWPORT
O papel e seu estado continuam em primeiro plano. Provedor e modelo aparecem agrupados, com o padrão global identificado. Loading, catálogo vazio, falha, catálogo desatualizado, ausência de modelo e bloqueio Git deixam a causa e a recuperação visíveis.

## FORM
Controles de 44 px, labels Code/Eval explícitos, foco visível, mensagens em português, navegação por teclado e respeito a redução de movimento. Desde 320 px, o grupo quebra em uma coluna sem cortar nomes longos.

## FINISH
Vitest prova herança do padrão, override em Code/Eval, API/Codex, falha de catálogo e ausência de gravação em Settings. Playwright cobre 320/768/900/1024/1440 e a execução do papel; Computer valida Code e Eval no desktop.
