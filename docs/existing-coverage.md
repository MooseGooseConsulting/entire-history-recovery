# Exact visible coverage reconciliation

This implements an initial part of slice B in the [rollout plan](plans/history-recovery.md).
The [goals](../GOALS.md) and [field catalog](entire-field-catalog.md) define the
project outcome and persisted data contract.

`internal/reconcile` is wired as a local preflight in the Codex recovery
prototype. It reads the checkpoint inventory before `agentimport.Run`; exact
full coverage under a different checkpoint ID is skipped, partial/conflicting
coverage is held, and read errors stop the import. It does not establish that a
remote or server inventory is complete.

`ReadVisible` hydrates all sessions in every listed checkpoint, including
names-only list stubs. A returned listing/read error, invalid summary, or
session without identity/transcript invalidates the whole result. The pinned
Entire store's `List` implementation can silently omit some unreadable refs or
checkpoint trees, and its remote enumeration is best-effort. This guard cannot
detect those omissions. Therefore the guard means “not observed in the locally
visible inventory,” not “absent everywhere”; remote/server completeness remains
unverified and this prototype must not be presented as global deduplication.

`AssessExact` compares a verified candidate with those records. It recognizes
matching session/agent/transcript/boundaries irrespective of checkpoint ID or
imported/live kind. `present` means every candidate boundary has a compatible
visible record; additional valid boundaries for that same transcript are neutral.
`hold` means conflicting or partial coverage needs reconciliation.
`unseen` only means not observed in this inventory. The current local prototype
continues to the upstream importer for unseen candidates, so this is a local
duplicate guard, not proof that a write is globally safe.

Inputs must be scoped to the same code repository and use the same sanitized,
redacted representation and verified raw-line boundaries. Repeated text at
different boundaries remains separate. Different redaction versions, partial
snapshots, compacted exports and native event alignment require the later
reconciliation layer; this helper deliberately holds instead of guessing.

Scoped tests cover multi-session/live-ID matching and fail-closed inventory
and partial-coverage behavior. Real-source integration remains a separate gate.
