---
version: 1
slug: "frontend-src-components-appshell-casual-mode"
primary_target: "frontend/src/components/AppShell.tsx"
related_targets: ["frontend/src/components/Sidebar.tsx", "frontend/src/components/WorkArea.tsx", "frontend/src/features/workbench/Workbench.tsx"]
---

# Casual and Professional workbench modes

## THESIS
Use one local workbench state in two arrangements: Casual returns developers to their project chats; Professional keeps the SDD control room visible.

## OWN-WORLD
Extend Harflex's Ion Mint world. Codex informs the chat-history hierarchy and search interaction only; Harflex keeps its own colors, controls, route names, evidence views, and local-first behavior.

## STORY
Open a project → start a chat or reopen a prior chat in Casual → reach any specialist tool from Ferramentas → switch to Professional to inspect the same session's SDD phase, artifacts, and activity.

## FIRST VIEWPORT
Casual shows the project name, New chat, a search field, recent session rows, and Ferramentas in the left sidebar; conversation and composer stay central. Professional keeps its current 16-item sidebar, pipeline rail, and activity panel.

## FORM
Keep the Professional structure unchanged. Casual uses a fixed readable history column at 768px and above, a drawer below 768px, list rows with a message-derived title plus state/date, and a collapsible list of the same 16 destinations. Activity opens on demand. Use existing tokens, 44px controls, visible focus, keyboard search, no permanent glow or new color system.

## FINISH
Test persisted mode, history load/search/replay, occupied-session protection, new chat, all 16 destinations, focus and Escape, and the same breakpoints as the product shell. Inspect both modes with non-empty history and an active pipeline.
