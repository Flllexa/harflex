---
version: 1
slug: "frontend-src-features-pipelines-pipelinedesignstudio-tsx"
primary_target: "frontend/src/features/pipelines/PipelineDesignStudio.tsx"
related_targets: ["frontend/src/features/pipelines/PipelinesPage.tsx", "frontend/src/styles/global.css"]
---

# Preparar trabalho — Operate

O desenvolvedor descreve uma demanda, reconhece os documentos preparados e ajusta texto/conversa antes de permitir Code. O usuário delegou a direção e pediu o fluxo familiar de harnesses para devs.

## Direction contract

**THESIS:** uma conversa mantém Discovery/SPEC/Plan vivos e visíveis; a preparação não pede que o usuário opere cada etapa de geração.

**OWN-WORLD:** Ion Mint, ciano para evidência, superfícies Forge/Ion Void, fontes do sistema, Lucide e foco de 44 px. Operador em sessão longa de código, com o tema escuro já escolhido pelo produto.

**STORY:** descrever → ver SPEC/Plan preparados → ajustar por conversa ou editor → aprovar os documentos para Code. Versões e dependências são visíveis sem expor IDs técnicos.

**FIRST VIEWPORT:** cabeçalho curto com título/estado; conversa à esquerda e documento à direita; abas Discovery/SPEC/Plan sobre o texto; composer e ação contextual na base. Histórico e modelos ficam recolhidos. Em 320 px a alternância Conversa/Documentos substitui a divisão.

**FORM:** workspace de conversa com documentos, derivado do fluxo preciso pedido pelo usuário; seed `user-delegated-conversation-documents`, build code-led dentro da identidade existente. A interação assinatura é pedir uma mudança e acompanhar quais documentos recebem nova versão.

**FINISH:** unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance. Sem raster novo previsto; validação nativa real e E2E em larguras suportadas, detector e crítico independente.
