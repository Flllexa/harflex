---
name: Harflex
description: Sala de controle local-first para desenvolvimento agentic orientado a SDD
colors:
  ion-void: "#080b0d"
  forge: "#12171a"
  border: "#273036"
  mint: "#5cf2a6"
  cyan: "#39bff8"
  paper: "#f1f5f4"
  muted: "#82908c"
  soft-mint: "#16382a"
  danger: "#f2777a"
  soft-danger: "#3a1c1e"
typography:
  headline:
    fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "22px"
    fontWeight: 650
    lineHeight: 1.3
  title:
    fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "14px"
    fontWeight: 650
    lineHeight: 1.5
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "12px"
    fontWeight: 600
    lineHeight: 1.5
  mono:
    fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.5
rounded:
  control: "8px"
  panel: "12px"
spacing:
  "1": "4px"
  "2": "8px"
  "3": "12px"
  "4": "16px"
  "6": "24px"
components:
  button-primary:
    backgroundColor: "{colors.mint}"
    textColor: "{colors.ion-void}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "44px"
  button-primary-hover:
    backgroundColor: "{colors.paper}"
  button-secondary:
    textColor: "{colors.paper}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "44px"
  input:
    backgroundColor: "{colors.ion-void}"
    textColor: "{colors.paper}"
    rounded: "{rounded.control}"
    padding: "8px 12px"
  nav-current:
    backgroundColor: "{colors.soft-mint}"
    textColor: "{colors.mint}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "44px"
  chip-local:
    textColor: "{colors.mint}"
    rounded: "{rounded.control}"
    padding: "2px 8px"
  tool-card:
    backgroundColor: "{colors.forge}"
    textColor: "{colors.paper}"
    rounded: "{rounded.control}"
    padding: "12px"
  pipeline-current:
    textColor: "{colors.mint}"
    typography: "{typography.label}"
---

# Design System: Harflex

## Overview

**Creative North Star: "Sala de Controle Auditável"**

Harflex parece um instrumento técnico preciso que permanece legível durante execuções longas. A interface usa superfícies escuras estratificadas, informação compacta e sinais luminosos com significado operacional. O pipeline, as evidências e o estado atual devem ser compreendidos antes dos detalhes decorativos.

A densidade e a navegação persistente reconhecem a referência LionClaw, mas a assinatura visual é própria: menta elétrica e ciano sobre obsidiana, composição mais disciplinada e ausência de laranja como cor estrutural. A personalidade vem da clareza dos estados, do ritmo dos dados e de transições que mostram causalidade.

**Key Characteristics:**

- Escura, precisa e operacional.
- Densa sem ser apertada.
- Progresso, risco e evidência visualmente distintos.
- Movimento usado para explicar mudança de estado.
- Controles familiares com acabamento próprio, nunca futurismo genérico.

## Colors

A estratégia é restrita: neutros frios ocupam a maior parte da tela; menta e ciano aparecem somente onde existe estado, ação ou evidência.

### Primary

- **Signal Mint** (`#5CF2A6`): ação principal, etapa concluída, execução saudável e foco ativo.

### Secondary

- **Evidence Cyan** (`#39BFF8`): links, evidências, atividade informativa e relações secundárias em gráficos.

Falhas e negações usam danger; soft-danger reserva o fundo de falha. Os valores canônicos estão no frontmatter e em `frontend/src/styles/tokens.css`.

### Neutral

- **Ion Void** (`#080B0D`): fundo estrutural da aplicação.
- **Forge Surface** (`#12171A`): painéis, navegação e cartões em repouso.
- **Interface Border** (`#273036`): divisores e limites funcionais.
- **Paper White** (`#F1F5F4`): texto principal e valores críticos.
- **Muted Sage** (`#82908C`): texto secundário e metadados.
- **Soft Mint** (`#16382A`): fundo de seleção e sucesso discreto.

**The Signal Has Meaning Rule.** Signal Mint não é decoração. Cada ocorrência deve indicar ação, progresso confirmado, foco ou saúde.

**The No Ambient Rainbow Rule.** Provedores, fases e ferramentas podem ter cores categóricas, mas nunca transformam a superfície em uma coleção de acentos concorrentes.

## Typography

A implementação usa pilhas do sistema: `-apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif` para interface e `ui-monospace, SFMono-Regular, Consolas, monospace` para caminhos, comandos e diff. Mona Sans e Commit Mono eram intenção do seed; não estão empacotadas nem carregadas.

A hierarquia está no frontmatter: headline no projeto, title nos grupos, body na conversa, label nos estados e mono nos artefatos. Subtítulos do setup usam 16 px; metadados menores usam 11 px. A conversa limita linhas a 72ch e quebra conteúdo longo.

**The Numbers Stay Still Rule.** Métricas, tokens, duração, custos, caminhos e logs usam numerais tabulares; valores dinâmicos não podem deslocar a composição.

## Layout

A composição aprovada organiza navegação, trabalho central e atividade. Desde 320 px, navegação e atividade são gavetas temporárias. A partir de 768 px, a navegação ocupa um trilho de 72 px; a partir de 1024 px, ocupa 240 px e a atividade aberta ocupa 320 px.

A partir de 768 px o shell ocupa exatamente a janela: o centro, a navegação e a atividade rolam sozinhos, o rodapé de status fica sempre visível e a conversa mantém o compositor fixo, com a lista de mensagens rolando por dentro e acompanhando a mensagem mais nova enquanto o leitor estiver no fim. Abaixo de 768 px a página rola normalmente. Cada destino abre no topo.

**A Drag Strip Is Not A Toolbar Rule.** O app desktop esconde a barra de título nativa e trata os 50 px superiores como faixa de arraste (`InvisibleTitleBarHeight`): nenhum controle interativo pode ficar nessa faixa. A primeira linha do cabeçalho apenas nomeia o destino; o seletor Casual/Professional e o botão de atividade ficam na linha seguinte, junto do pipeline. No macOS, os semáforos da janela flutuam sobre o canto superior esquerdo, então a navegação reserva uma área segura (`html[data-platform="mac"]`).

Navegação agrupada (trabalho, automação, contexto, sistema) cabe em 900 px sem rolagem. Alvos de 44 px valem para toque e abaixo de 768 px; com mouse em janelas largas, linhas compactas de 32 px mantêm os 17 destinos à vista.

O centro usa `minmax(0, 1fr)`; o pipeline admite rolagem interna e mantém a etapa atual visível após resize. Espaços seguem a escala do frontmatter. Ações principais têm pelo menos 44 × 44 px; títulos e caminhos quebram linha. O diálogo mede no máximo 440 px, respeita 16 px de margem e rola verticalmente quando necessário.

## Elevation & Depth

O sistema é plano por padrão. Profundidade nasce de camadas tonais, bordas finas e oclusão clara; sombras aparecem somente em superfícies temporárias como menus, diálogos e gavetas flutuantes. Não há vidro fosco, brilho ambiental ou halos neon permanentes.

**The Flat Until Lifted Rule.** Uma superfície só recebe sombra quando realmente se move acima de outra.

Sombras observadas: gaveta esquerda (`12px 0 40px #0008`), direita (`-12px 0 40px #0008`) e diálogo (`0 24px 64px #000a`). O scrim usa `#0009`.

## Shapes

Controles pequenos usam cantos de 6–8 px; painéis e cartões usam 10–12 px. Pills ficam restritas a estados, filtros e categorias curtas. Bordas são contínuas e discretas; recortes decorativos, cápsulas gigantes e cartões excessivamente arredondados não pertencem ao produto.

## Components

Botão primário: fundo mint, texto ion-void, peso 650, altura mínima 44 px e hover paper. Secundário: borda border e hover border; desabilitados têm opacidade 0.55 e cursor de indisponibilidade.

Campos: fundo ion-void, borda border, padding da variante input e altura mínima 44 px. Foco usa contorno sólido mint de 2 px, offset de 1 px em campos e 3 px nos demais controles. Erros de formulário usam danger.

Navegação: seleção soft-mint com texto mint. O trilho esconde apenas o rótulo visual; a gaveta preserva nome acessível, Escape e retorno de foco. Chip local: texto mint e borda soft-mint, sem interação.

Cartão de ferramenta: superfície forge, nome, caminho, status e saída expansível; o diff gravado aparece dentro do cartão. Aprovação separa risco, destino, **o que será feito** (conteúdo a gravar, substituição ou comando completo) e decisão, e fica fixa acima do compositor. Diff usa mint para adições, danger para remoções e cyan para hunks.

Respostas do agente usam Markdown renderizado como elementos (sem HTML bruto): títulos a partir de h3, listas, tabelas, citações, código em bloco com botão de copiar e links externos abertos no navegador do sistema.

Worktrees: cada linha é um cartão forge com borda esquerda de 3 px que dá o veredito de relance (mint para pode excluir, danger para não pode, cyan para o principal), nome da branch, caminho curto, fatos (alterações, commits à frente, mesclado) e, em coluna própria, o veredito escrito com os motivos em lista. Os botões ficam à direita em telas largas e abaixo do texto em telas estreitas; excluir usa o botão danger (borda soft-danger, texto danger), e **Salvar e mesclar com a IA…** é o único primário da linha. Itens ignorados pelo Git aparecem num bloco danger com confirmação explícita antes de qualquer exclusão. O cabeçalho do projeto repete o estado do worktree numa linha só (branch, fatos, veredito, ações) e mostra um aviso de execução com papel `status` enquanto a IA trabalha ou terminou, ou `alert` quando algo ficou pendente.

Páginas de catálogo (agentes, skills, MCP, workflows, agendamentos) começam pela lista; o formulário de criação aparece aberto quando não há itens e fica a um clique (`Novo …`) quando há.

Pipeline: lista horizontal com etapa atual mint e `aria-current="step"`; etapas pendentes usam muted. Tabs mantêm estado e evidências da sessão. Transições de fundo duram 120 ms com ease-out. `prefers-reduced-motion: reduce` remove transições/animações e fixa scroll automático.

## Do's and Don'ts

### Do:

- **Do** manter o pipeline e o estado atual identificáveis em um olhar.
- **Do** usar a cor para semântica operacional e preservar grandes áreas neutras.
- **Do** revelar detalhes progressivamente em gavetas, inspeções e painéis contextuais.
- **Do** mostrar loading, vazio, erro, permissão, execução, pausa, bypass e conclusão como estados completos.

### Don't:

- **Don't** copiar marca, ícones, textos ou composição pixel a pixel do LionClaw.
- **Don't** usar glassmorphism, gradientes ornamentais, halos neon ou cards dentro de cards sem necessidade estrutural.
- **Don't** esconder risco, custo, permissões ou falhas atrás de linguagem vaga.
- **Don't** reduzir a experiência a um chat genérico com uma barra lateral decorativa.
