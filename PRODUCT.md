# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Developers and technical professionals who want to delegate complete work to AI agents in a local desktop app, while keeping visibility and control over each stage. The first distribution must serve macOS, Windows, and Linux.

## Product Purpose

Harflex is a local-first desktop harness for talking to agents, developing software, and automating work on the user's own machine. Its agent engine will be implemented natively in Go, inspired by the contracts and behaviors of Pi. The main development flow follows Specification-Driven Development (SDD), making discovery, specification, planning, implementation, and evaluation visible and resumable.

SDD is the default path, but the user may consciously skip steps. The product must record the bypass so that execution stays auditable.

## Positioning

The product combines an extensible runtime native to Go, compatible with model APIs and CLI agents, with a visual desktop experience oriented to SDD. Instead of hiding execution in a linear chat, it exposes state, decisions, evidence, agents, tools, cost, tokens, and results as navigable parts of the work.

## Operating Context

- The app works on repositories and directories chosen by the user.
- Sessions, projects, skills, knowledge, metrics, and settings stay on the local machine.
- The network is used only when needed for AI providers, MCP servers, and connectors enabled by the user.
- The user can run interactive flows, follow long executions, review changes, and resume work after restarting the app.

## Capabilities and Constraints

- Conceptual basis: Pi's public contracts and behaviors will be reimplemented in Go, preserving the attribution and notices required by the MIT license when derived code is involved.
- Engine: agent loop, streaming, tool calling, branchable sessions, compaction, skills, extensions, and telemetry implemented in Go.
- Native model integrations: OpenAI, OpenRouter, and compatible APIs, with an architecture ready for Anthropic, Google, Ollama, LM Studio, and other providers.
- External agent integrations: adapters for Codex CLI, Claude Code, OpenCode, and other CLIs, with explicit capability discovery.
- Code tools: functional parity with Pi's `read`, `write`, `edit`, `bash`/PowerShell, `grep`, `find`, and `ls`, plus Harflex's permission policies and audit.
- Desktop shell: Wails with a Go backend and a web frontend.
- Distribution: macOS, Windows, and Linux.
- Providers: multiple AI providers, including remote services and compatible local models.
- SDD: a default pipeline with explicit phases, acceptance criteria, implementation, and evaluation; the user may skip steps, with a record of the reason and the resulting state.
- Extensibility: subagents, skills, tools, MCP servers, and local automations.
- Persistence: local-first, resumable, and without a mandatory proprietary backend.
- Security: no credential may be stored in plain text; terminal, file, network, and integration actions require configurable policies and consent.
- Features inspired by the LionClaw reference must be reimplemented with their own identity, without copying proprietary assets or claiming unvalidated compatibility.
- The full scope will be delivered in functional increments, keeping every capability approved in the roadmap and validating each increment end to end.

## Brand Commitments

- Working name: Harflex.
- Primary interface language: Brazilian Portuguese, with an architecture ready for localization.
- Functional and operational-density reference: LionClaw.
- The interface must have its own identity; the reference guides hierarchy, readability, and pipeline transparency, not a literal copy.

## Evidence on Hand

- The public Pi repository provides an agent runtime, a multi-provider API, sessions, tools, skills, extensions, telemetry, and RPC/JSONL integration.
- The public LionClaw page describes local chat, multi-provider subagents, skills, MCPs, local knowledge, development pipelines, and per-phase metrics.
- The reference image shows side navigation, an SDD pipeline in stages, sprints, aggregated metrics, and cost per stage in a dark desktop interface.
- There is still no final identity, logo, commercial content, own benchmarks, or real user data. Future work must not fabricate them.

## Product Principles

1. Local by default, network by consent.
2. SDD visible and resumable, never a black box.
3. Power with explicit limits: permissions, isolation, and audit accompany automation.
4. Extension without lock-in: providers, models, skills, and MCPs remain replaceable.
5. Evidence before success: each stage shows artifacts, checks, and the real result.

## Accessibility & Inclusion

The interface must work with the keyboard, expose visible focus, respect reduced motion, maintain adequate contrast, and remain usable from 320 px up to wide desktop screens. Touch targets must be at least 44 px on touch surfaces.
