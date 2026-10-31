# Implementation notes

## Deviations

- Security scope is recorded in [GOALS.md](../../GOALS.md#security-scope). The exploratory ancestor-ACL guard remains outside the implementation stack; no existing host ACLs were changed. Broad host hardening must not displace the recovery and live verification work.

- Earlier work treated SER10 authentication as blocking server proof. The revised plan uses the authenticated Windows CLI and keeps host choice independent of source discovery.
- Existing deterministic checkpoint IDs only establish repeat-import behavior for visible imported records. The plan now requires remote/server inventory and native-live session coverage reconciliation before real production writes.
- The first native Codex parser splits injected user-role context into importer units. The 14-record local result is not a claim of 14 human prompts.

## Review

- Code-grounding review: checked `ImportCodex` options against v0.12.2 `agentimport.Run`, including its swallowed listing error and missing remote callbacks in our wrapper.
- Verified an inherited organization ruleset exists. Legacy branch-protection lookup returns 404; that is not evidence that checks are optional.
- Fresh-reader plan review completed by an independent agent given only the repository and plan paths. Addressed its findings: explicit snapshot sealing and quiescence; independent checkpoint-destination preview; display-only anchor policy and empty-repo behavior; absent-original-path assertions; three-checkout remote-only dedup fixture; append-only partial coverage with collision holds; and separation of safe HOLD tests from successful server reconciliation.

## Implementation review loop

- Work is split into non-draft stacked PRs. Implementation agents own separate worktrees; an independent trailing agent verifies whether review comments identify a real defect. No PR in this implementation stack has been merged during this pass.
- Fixed a valid coverage-review finding: extra valid existing turn boundaries no longer make a fully covered candidate look incomplete; invalid offsets still hold.
- Snapshot review found stat-only source verification could miss a same-size rewrite and existing snapshot reuse could follow a symlink. The implementation now rehashes the open source and rejects symlink/non-regular snapshot paths. The trailing reviewer verified both fixes and their regressions.
- Cohort review required pinning the selected manifest and checking model values per session/boundary, beyond counting nonempty values. These checks are part of the cohort acceptance work, not evidence that native turn semantics are fully understood.

## Current evidence and remaining work

Twelve actual inactive single-header sessions produced 37 local checkpoint records. Both the process-isolated cohort path and the separate sealed CLI path performed actual second writes: zero new records, 37 skipped. Original hashes and checkpoint refs were unchanged. The CLI run left no staging files and its temporary repository was removable after process exit. Real sources, source paths, manifests and receipts remain private.

A subsequent retained local destination was verified using installed Entire CLI 0.12.1: checkpoint list changed from 0 to 37 and remained 37 after retrying the same 12 sessions. Native detail readback covered all 37 records and the 12 original session identities; stored transcripts and a full rendered conversation were inspected privately. The retained repository has no remotes. This is native local CLI evidence, not hosted Entire visibility. Its location and raw captures are kept in the private receipt rather than this public repository.

The full synthetic suite and vet passed on SER10 Linux at `2bd2a00`, including the newly connected local coverage guard; no private corpus was transferred. Windows snapshot tests and the deliberate child-process cleanup acceptance pass. The original in-process Windows test cleanup problem remains unresolved; process exit is the verified operating boundary.

Local exact-coverage assessment now guards writes, including records with different checkpoint IDs. Server ingestion, authoritative existing-history inventory, broader semantic reconciliation, verified historical repository mapping, resumable receipts and bulk rollout remain incomplete. The local CLI does not publish checkpoints. Review and merge of the open stack remain pending. Next work must close those integration gates before treating the 12-session local result as permission for production ingestion.
