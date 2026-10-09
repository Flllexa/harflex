---
name: Harflex
description: Local-first control room for SDD-oriented agentic development
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

**Creative North Star: "Auditable Control Room"**

Harflex feels like a precise technical instrument that stays legible during long runs. The interface uses layered dark surfaces, compact information, and luminous signals with operational meaning. The pipeline, the evidence, and the current state must be understood before any decorative detail.

The density and persistent navigation acknowledge the LionClaw reference, but the visual signature is its own: electric mint and cyan over obsidian, a more disciplined composition, and no orange as a structural color. The personality comes from the clarity of the states, the rhythm of the data, and transitions that show cause and effect.

**Key Characteristics:**

- Dark, precise, and operational.
- Dense without being cramped.
- Progress, risk, and evidence visually distinct.
- Motion used to explain state changes.
- Familiar controls with their own finish, never generic futurism.

## Colors

The strategy is restrained: cool neutrals take up most of the screen; mint and cyan appear only where there is state, action, or evidence.

### Primary

- **Signal Mint** (`#5CF2A6`): primary action, completed stage, healthy execution, and active focus.

### Secondary

- **Evidence Cyan** (`#39BFF8`): links, evidence, informative activity, and secondary relations in charts.

Failures and denials use danger; soft-danger is reserved for the failure background. The canonical values are in the frontmatter and in `frontend/src/styles/tokens.css`.

### Neutral

- **Ion Void** (`#080B0D`): the application's structural background.
- **Forge Surface** (`#12171A`): panels, navigation, and cards at rest.
- **Interface Border** (`#273036`): dividers and functional boundaries.
- **Paper White** (`#F1F5F4`): primary text and critical values.
- **Muted Sage** (`#82908C`): secondary text and metadata.
- **Soft Mint** (`#16382A`): selection background and discreet success.

**The Signal Has Meaning Rule.** Signal Mint is not decoration. Each occurrence must indicate action, confirmed progress, focus, or health.

**The No Ambient Rainbow Rule.** Providers, phases, and tools may have categorical colors, but they never turn the surface into a collection of competing accents.

## Typography

The implementation uses system stacks: `-apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif` for the interface and `ui-monospace, SFMono-Regular, Consolas, monospace` for paths, commands, and diffs. Mona Sans and Commit Mono were the seed's intent; they are neither packaged nor loaded.

The hierarchy is in the frontmatter: headline for the project, title for groups, body in the conversation, label for states, and mono for artifacts. Setup subtitles use 16 px; smaller metadata uses 11 px. The conversation limits lines to 72ch and breaks long content.

**The Numbers Stay Still Rule.** Metrics, tokens, durations, costs, paths, and logs use tabular numerals; dynamic values must not shift the composition.

## Layout

The approved composition organizes navigation, central work, and activity. From 320 px, navigation and activity are temporary drawers. From 768 px, navigation takes a 72 px rail; from 1024 px, it takes 240 px, and an open activity panel takes 320 px.

From 768 px the shell fills the window exactly: the center, the navigation, and the activity scroll on their own, the status footer stays always visible, and the conversation keeps the composer fixed, with the message list scrolling inside and following the newest message while the reader is at the end. Below 768 px the page scrolls normally. Each destination opens at the top.

**A Drag Strip Is Not A Toolbar Rule.** The desktop app hides the native title bar and treats the top 50 px as a drag strip (`InvisibleTitleBarHeight`): no interactive control may sit in that strip. The first line of the header only names the destination; the Casual/Professional selector and the activity button sit on the next line, together with the pipeline. On macOS, the window's traffic-light buttons float over the top-left corner, so navigation reserves a safe area (`html[data-platform="mac"]`).

Grouped navigation (work, automation, context, system) fits in 900 px without scrolling. 44 px targets apply to touch and below 768 px; with a mouse on wide windows, compact 32 px rows keep the 17 destinations in view.

The center uses `minmax(0, 1fr)`; the pipeline supports internal scrolling and keeps the current stage visible after a resize. Spacing follows the frontmatter scale. Primary actions are at least 44 × 44 px; titles and paths wrap. The dialog measures at most 440 px, keeps a 16 px margin, and scrolls vertically when needed.

## Elevation & Depth

The system is flat by default. Depth comes from tonal layers, thin borders, and clear occlusion; shadows appear only on temporary surfaces such as menus, dialogs, and floating drawers. There is no frosted glass, ambient glow, or permanent neon halo.

**The Flat Until Lifted Rule.** A surface only receives a shadow when it actually moves above another surface.

Observed shadows: left drawer (`12px 0 40px var(--shadow)`), right drawer (`-12px 0 40px var(--shadow)`), and dialog (`0 24px 64px var(--shadow)`). The scrim uses `var(--shadow)`; the token changes with the light or dark theme.

## Shapes

Small controls use corners of 6–8 px; panels and cards use 10–12 px. Pills are restricted to states, filters, and short categories. Borders are continuous and discreet; decorative cutouts, giant capsules, and excessively rounded cards do not belong to the product.

## Components

Primary button: mint background, ion-void text, weight 650, minimum height 44 px, and paper on hover. Secondary: Interface Border and a border highlight on hover; disabled controls have an opacity of 0.55 and an unavailable cursor.

Fields: ion-void background, Interface Border, the padding of the input variant, and a minimum height of 44 px. Focus uses a solid 2 px mint outline, offset by 1 px on fields and 3 px on other controls. Form errors use danger.

Navigation: soft-mint selection with mint text. The rail hides only the visual label; the drawer keeps the accessible name, Escape, and focus return. Local chip: mint text and soft-mint border, with no interaction.

Tool card: forge surface, name, path, status, and an expandable output; the recorded diff appears inside the card. Approval separates the risk, the target, **what will be done** (the content to write, the replacement, or the full command), and the decision, and stays fixed above the composer. The diff uses mint for additions, danger for removals, and cyan for hunks.

Agent responses use Markdown rendered as elements (no raw HTML): headings from h3, lists, tables, quotes, code blocks with a copy button, and external links that open in the system browser.

Worktrees: each row is a forge card with a 3 px left border that gives the verdict at a glance (mint for can delete, danger for cannot, cyan for the main one), the branch name, a short path, facts (changes, commits ahead, merged) and, in its own column, the verdict written out with its reasons as a list. Buttons sit on the right on wide screens and below the text on narrow ones; delete uses the danger button (soft-danger border, danger text), and **Save and merge with AI…** is the only primary button in the row. Git-ignored items appear in a danger block with explicit confirmation before any deletion. The project header repeats the worktree state on one line (branch, facts, verdict, actions) and shows a run notice with the `status` role while the AI works or has finished, or the `alert` role when something remained pending.

Catalog pages (agents, skills, MCP, workflows, schedules) start with the list; the creation form appears open when there are no items and sits one click away (`New …`) when there are.

Pipeline: horizontal list with the current stage in mint and `aria-current="step"`; pending stages use muted. Tabs keep the session's state and evidence. Background transitions last 120 ms with ease-out. `prefers-reduced-motion: reduce` removes transitions and animations and fixes automatic scrolling.

## Do's and Don'ts

### Do:

- **Do** keep the pipeline and the current state identifiable at a glance.
- **Do** use color for operational meaning and preserve large neutral areas.
- **Do** reveal details progressively in drawers, inspections, and contextual panels.
- **Do** show loading, empty, error, permission, execution, pause, bypass, and completion as complete states.

### Don't:

- **Don't** copy LionClaw's brand, icons, texts, or composition pixel by pixel.
- **Don't** use glassmorphism, ornamental gradients, neon halos, or cards inside cards without structural need.
- **Don't** hide risk, cost, permissions, or failures behind vague language.
- **Don't** reduce the experience to a generic chat with a decorative sidebar.
