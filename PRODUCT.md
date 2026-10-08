# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Desenvolvedores e profissionais técnicos que querem delegar trabalho completo a agentes de IA em um aplicativo desktop local, mantendo visibilidade e controle sobre cada etapa. A primeira distribuição deve atender macOS, Windows e Linux.

## Product Purpose

Harflex é um harness desktop local-first para conversar com agentes, desenvolver software e automatizar trabalho na própria máquina. Seu engine de agentes será implementado nativamente em Go, inspirado nos contratos e comportamentos do Pi. O principal fluxo de desenvolvimento segue Specification-Driven Development (SDD), tornando descoberta, especificação, planejamento, implementação e avaliação visíveis e retomáveis.

O SDD é o caminho padrão, mas o usuário pode pular etapas conscientemente. O produto deve registrar o bypass para que a execução continue auditável.

## Positioning

O produto combina um runtime extensível nativo em Go, compatível com APIs de modelos e agentes CLI, com uma experiência desktop visual orientada a SDD. Em vez de esconder a execução em um chat linear, ele expõe estado, decisões, evidências, agentes, ferramentas, custo, tokens e resultados como partes navegáveis do trabalho.

## Operating Context

- O aplicativo trabalha sobre repositórios e diretórios escolhidos pelo usuário.
- Sessões, projetos, skills, conhecimento, métricas e configurações permanecem na máquina local.
- A rede é usada somente quando necessária para provedores de IA, servidores MCP e conectores habilitados pelo usuário.
- O usuário pode executar fluxos interativos, acompanhar execuções longas, revisar mudanças e retomar trabalho após reiniciar o aplicativo.

## Capabilities and Constraints

- Base conceitual: contratos e comportamentos públicos do Pi serão reimplementados em Go, preservando atribuição e avisos exigidos pela licença MIT quando houver código derivado.
- Engine: loop de agente, streaming, tool calling, sessões ramificáveis, compactação, skills, extensões e telemetria implementados em Go.
- Integrações nativas de modelos: OpenAI, OpenRouter e APIs compatíveis, com arquitetura para Anthropic, Google, Ollama, LM Studio e outros provedores.
- Integrações de agentes externos: adaptadores para Codex CLI, Claude Code, OpenCode e outros CLIs com descoberta explícita de capacidades.
- Ferramentas de código: paridade funcional com `read`, `write`, `edit`, `bash`/PowerShell, `grep`, `find` e `ls` do Pi, acrescida de políticas de permissão e auditoria do Harflex.
- Shell desktop: Wails com backend em Go e frontend web.
- Distribuição: macOS, Windows e Linux.
- Provedores: múltiplos provedores de IA, inclusive serviços remotos e modelos locais compatíveis.
- SDD: pipeline padrão com fases explícitas, critérios de aceite, implementação e avaliação; etapas podem ser puladas pelo usuário com registro do motivo e do estado resultante.
- Extensibilidade: subagentes, skills, ferramentas, servidores MCP e automações locais.
- Persistência: local-first, retomável e sem backend proprietário obrigatório.
- Segurança: nenhuma credencial deve ser armazenada em texto puro; ações de terminal, arquivos, rede e integrações precisam de políticas e consentimento configuráveis.
- Funcionalidades inspiradas na referência LionClaw devem ser reimplementadas com identidade própria, sem copiar ativos proprietários ou alegar compatibilidade não validada.
- O escopo completo será entregue em incrementos funcionais, mantendo todas as capacidades aprovadas no roadmap e validando cada incremento de ponta a ponta.

## Brand Commitments

- Nome de trabalho: Harflex.
- Idioma principal da interface: português do Brasil, com arquitetura pronta para localização.
- Referência funcional e de densidade operacional: LionClaw.
- A interface deve ter identidade própria; a referência orienta hierarquia, legibilidade e transparência do pipeline, não uma cópia literal.

## Evidence on Hand

- O repositório público Pi fornece runtime de agente, API multiprovider, sessões, ferramentas, skills, extensões, telemetria e integração RPC/JSONL.
- A página pública do LionClaw descreve chat local, subagentes multiprovider, skills, MCPs, conhecimento local, pipelines de desenvolvimento e métricas por fase.
- A imagem de referência mostra navegação lateral, pipeline SDD em etapas, sprints, métricas agregadas e custo por etapa em uma interface desktop escura.
- Ainda não existem identidade final, logotipo, conteúdo comercial, benchmarks próprios nem dados reais de usuários. Trabalho futuro não deve fabricá-los.

## Product Principles

1. Local por padrão, rede por consentimento.
2. SDD visível e retomável, nunca uma caixa-preta.
3. Poder com limites explícitos: permissões, isolamento e auditoria acompanham a automação.
4. Extensão sem aprisionamento: provedores, modelos, skills e MCPs permanecem substituíveis.
5. Evidência antes de sucesso: cada etapa mostra artefatos, verificações e resultado real.

## Accessibility & Inclusion

A interface deve funcionar por teclado, expor foco visível, respeitar redução de movimento, manter contraste adequado e permanecer utilizável de 320 px até telas desktop amplas. Alvos de toque devem ter no mínimo 44 px em superfícies touch.
