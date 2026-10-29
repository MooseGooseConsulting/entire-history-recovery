# Implementation notes

## Deviations

- Earlier work treated SER10 authentication as blocking server proof. The revised plan uses the authenticated Windows CLI and keeps host choice independent of source discovery.
- Existing deterministic checkpoint IDs only establish repeat-import behavior for visible imported records. The plan now requires remote/server inventory and native-live session coverage reconciliation before real production writes.
- The first native Codex parser splits injected user-role context into importer units. The 14-record local result is not a claim of 14 human prompts.

## Review

- Code-grounding review: checked `ImportCodex` options against v0.12.2 `agentimport.Run`, including its swallowed listing error and missing remote callbacks in our wrapper.
- Verified an inherited organization ruleset exists. Legacy branch-protection lookup returns 404; that is not evidence that checks are optional.
- Fresh-reader plan review completed by an independent agent given only the repository and plan paths. Addressed its findings: explicit snapshot sealing and quiescence; independent checkpoint-destination preview; display-only anchor policy and empty-repo behavior; absent-original-path assertions; three-checkout remote-only dedup fixture; append-only partial coverage with collision holds; and separation of safe HOLD tests from successful server reconciliation.

## How the run ended

Plan revision written and fresh-reader feedback incorporated. Core local recovery and installed-CLI readback demonstrated; server ingestion, full duplicate coverage, Windows cleanup resolution and bulk rollout remain incomplete. No new production checkpoint publication is performed by this documentation change. PR review/merge is a separate pending step.
