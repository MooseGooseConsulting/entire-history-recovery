# Exact visible coverage reconciliation

`internal/reconcile` is a read-only building block. It is not yet wired to the
import command and does not establish that a remote or server inventory is
complete.

`ReadVisible` hydrates all sessions in every listed checkpoint, including
names-only list stubs. A failed listing, summary or session read invalidates the
whole result. Store-level silent omissions cannot be detected by this function;
the production inventory must establish completeness separately.

`AssessExact` compares a verified candidate with those records. It recognizes
matching session/agent/transcript/boundaries irrespective of checkpoint ID or
imported/live kind. `present` means every candidate boundary has a compatible
visible record; `hold` means conflicting or partial coverage needs reconciliation.
`unseen` only means not observed in this inventory and never authorizes a write.

Inputs must be scoped to the same code repository and use the same sanitized,
redacted representation and verified raw-line boundaries. Repeated text at
different boundaries remains separate. Different redaction versions, partial
snapshots, compacted exports and native event alignment require the later
reconciliation layer; this helper deliberately holds instead of guessing.

Two scoped tests cover multi-session/live-ID matching and fail-closed inventory
and partial-coverage behavior. Real-source integration remains a separate gate.
