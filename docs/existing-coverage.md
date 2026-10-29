# Exact visible coverage reconciliation

This implements an initial part of slice B in the [rollout plan](plans/history-recovery.md).
The [goals](../GOALS.md) and [field catalog](entire-field-catalog.md) define the
project outcome and persisted data contract.

`AssessCodexStorage` is the Codex recovery preflight. It enumerates both pinned
Entire Git layouts directly: the v1 metadata branch and per-checkpoint refs.
It reads every checkpoint summary and every session metadata record, then reads
full transcript bodies only for sessions with the candidate's session ID.
Exact full coverage under a different checkpoint ID is skipped;
partial/conflicting coverage and inventory errors are held before import.

The preflight does not trust upstream `PersistentStore.List` or
`WalkCheckpointShards`, because both intentionally skip some unreadable or
malformed entries. It validates Git refs, commit/tree targets, shard structure,
root summaries, and all session metadata itself. For each configured Entire
checkpoint read target, it requires a successful `ls-remote` and fetches into a
temporary bare repository; it verifies fetched ref hashes and removes the
temporary repository. It does not add, advance, or delete refs in the checkout.
Any target listing, fetch, or parse failure stops the import.

`AssessExact` compares a verified candidate with matching session records. It
recognizes session/agent/transcript/boundaries irrespective of checkpoint ID or
imported/live kind. `present` means every candidate boundary has compatible
Git-backed coverage; `hold` means a conflict or partial coverage requires
reconciliation. `unseen` means only that this checkout and every configured
checkpoint Git target were enumerated without finding an exact match.

Inputs must be scoped to the same code repository and use the same sanitized,
redacted representation and verified raw-line boundaries. Repeated text at
different boundaries remains separate. Different redaction versions, partial
snapshots, compacted exports and native event alignment require the later
reconciliation layer; this helper deliberately holds instead of guessing.

The recovery adapter uses Entire's `transcript.IsInjectedPrompt` classifier to
exclude injected Codex preambles from prompt metadata while preserving the
complete source transcript. If a session contains only injected user-side
context but includes assistant or tool work, it writes one evidence checkpoint
with an empty prompt and the first original turn's boundary. It skips injected
context without assistant/tool work. Import counts therefore measure checkpoint
turn records, not human-authored prompts.

The Entire API's ingest/index state is not covered by Git inventory. This
prototype does not claim service-wide absence, complete server ingestion, or
cloud deduplication. `unseen` is not proof that no server-only record exists.
Existing tests cover different checkpoint IDs, live-style records, damaged
inventories, and a three-checkout remote-only retry for both Git backends.
Real-source and authenticated service readback remain separate gates.
