---
version: 2
slug: "frontend-src-features-pipelines-authoringstage-tsx"
primary_target: "frontend/src/features/pipelines/AuthoringStage.tsx"
related_targets: ["frontend/src/features/pipelines/PipelinesPage.tsx"]
---

# SPEC e Plan — extensão aprovada do Operate

## THESIS
Ler um documento gerado, escolher conscientemente modelo/esforço para a próxima tentativa SPEC ou Plan, e tomar uma decisão humana explícita com evidência da tentativa.

## OWN-WORLD
Ion Mint existente: shell, tokens, fontes e controles atuais. A referência harflex-conversation-evidence-workbench orienta a separação documento/evidência, sem reproduzir novas colunas ou redesenhar o shell.

## STORY
Discovery aprovada → preferência da fase lida localmente → consulta explícita de catálogo se necessária → preferência salva por CAS → comando explícito de geração → documento por seções → aprovação, revisão com feedback ou pulo justificado. Histórico mantém versão, fonte e sessão. GET/mount não consulta provedores nem inicia inferência; catálogo parcial/stale/unconfigured bloqueia sem fallback.

## FIRST VIEWPORT
Título SPEC/Plan, estado persistido e versão precedem o conteúdo. Um painel inline “Modelo e esforço desta fase” fica imediatamente antes das ações: modelo mostra “Herdar” e a origem (Brainstorm/Configurações) ou permite provedor/modelo específico; esforço oferece “Herdar”, “Automático” e só níveis anunciados pelo catálogo completo. A ação principal é clara. Stale/unconfigured substitui geração por estado/recuperação; cancelamento incerto substitui ações conflitantes por aviso e leitura de estado.

## FORM
Documento de até 72ch, seções separadas por espaço, metadados discretos e histórico recolhível. Reusar IonPicker e tokens existentes: controles de pelo menos 44px, provedor/modelo em duas colunas a partir de 768px e empilhados abaixo disso, esforço com “Herdar” distinto de “Automático” (que omite o campo). “Consultar modelos” é explícito; somente catálogo completo/fresco habilita override/esforço específico. IDs e credenciais nunca aparecem. Sem edição manual, modal novo ou redesign do shell. Layout de 320px a desktop.

## FINISH
Vitest para herança/override, CAS, erro/stale, catálogo com/sem níveis, zero requests no mount, teclado e estados de loading/empty/error/partial. Playwright 320/768/900/1024/1440 com teclado, foco, toque de 44px e reduced-motion. Inspeção visual em lote, detector uma vez e revisão independente antes do commit. Não atualizar o sidecar design.json nesta unidade.
