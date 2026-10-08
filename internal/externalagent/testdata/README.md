# Synthetic CLI protocol fixtures

These records contain invented prompts, identifiers and answers. No provider or
authenticated CLI is invoked by the tests.

- `codex-exec.jsonl` follows the official [Codex JSONL examples](https://learn.chatgpt.com/docs/non-interactive-mode#make-output-machine-readable): `thread.started.thread_id` and `item.completed.item` with `type: agent_message` and `text`. Started/updated items and non-message output remain audit events.
- `opencode-run.jsonl` follows the official [OpenCode run command](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/cli/cmd/run.ts), inspected on 2026-09-26: `emit("text", { part })` wraps a completed `message.part.updated` text snapshot, together with `sessionID`. The CLI does not forward SDK `message.part.delta` records as text events. Repeated, growing, shrinking and empty snapshots are synthetic resilience cases, not a claim that a specific CLI version emits all of them.
- `*-events.json` are the expected persisted `external.event` payloads. Go tests compare the fake subprocess/session output against these exact objects. Application integration runs the same synthetic process through the real journal and SQLite, verifies emission from a second connection after commit, then closes/reopens the database and checks durable replay. Frontend tests and browser replay consume the same fixtures.

Text parts use the part ID, falling back to the message ID when necessary. Only
identical consecutive snapshots preserve just raw audit data. JSONL order is
authoritative: any different snapshot replaces the part with its complete text,
including shorter or empty text. An empty replacement removes the chat bubble;
the durable replacement record remains available for replay. Normalization never
synthesizes suffix deltas that could split sensitive text between records.
Without a usable ID, nonempty text is a separate message and empty text remains
raw. Unknown event shapes never become assistant text.
