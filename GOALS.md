# Goals

The authoritative execution sequence, deduplication contract, review gates and readiness estimates are in [the implementation and rollout plan](docs/plans/history-recovery.md). The milestones below describe the project outcome; use that plan for current implementation status and sequencing.

Make historical agent work useful in Entire: ingest genuine recovered sessions into the correct repository, preserve the information Entire can represent, and establish commit attribution where evidence supports it. Deliver an accurate view of landed changes, including commits that lack an Entire checkpoint.

## Order of work

**Prove the import path first.** A machine-wide discovery effort is premature until a recovered session can be written, synced, and read back through Entire. Use one synthetic session and one representative real session with a known destination. Do not claim that an importer works based on local output alone.

1. **PR 1 — importer prototype.** Try a standalone tool that wraps Entire's existing importer interfaces, using a synthetic transcript, an isolated repository, a historical path that no longer exists, and an old timestamp. Preserve agent and model identity when known, turn boundaries, redaction, and stable IDs. Verify a second import does not duplicate records. Investigate a small, pinned fork or patch only if the wrapper cannot safely reach the needed functionality; record the reason and tradeoff.
2. **PR 2 — actual Entire round trip.** Extend the prototype to sync one correctly mapped, real historical session to its intended checkpoint destination, then verify it using Entire's authenticated server, search or explain, and a fresh checkout. Check the stored agent, model, timestamps, content, and redaction behavior. Establish whether the imported session appears in dispatch; do not assume it does. Keep source transcripts and real evidence out of the public project.
3. **PR 3 — recover commit attribution.** Prove an evidence-backed link between a recovered session and an existing commit without rewriting shared history. Inspect how Entire verifies trailerless links, and test both a squash merge and a session with no provable link. An import display anchor is not attribution.
4. **PR 4 onward — scale source discovery and ingestion.** Only once the real import round trip works, add manifest-driven discovery, historical repository mapping, deduplication, format support, and resumable batch processing. Use existing source inventories and indexes first. Deleted checkouts must not be required; uncertain mappings must not become invented links. Split further PRs by working capability as scope becomes clear.
5. **Dispatch and completion.** Determine whether Entire's cloud dispatch can accurately represent recovered checkpoints and all landed commits. If it cannot, implement a GitHub-backed dispatch enriched with verified Entire records. Verify the same time window against actual Git history and report failures rather than treating missing data as inactivity. Reconcile adapter changes and retire temporary scratch materials after preserving unique work.

## Definition of working

- A historical session whose original checkout is gone appears under the correct Entire repository with its real agent and source-backed metadata.
- Its checkpoint survives sync and can be read back from a fresh checkout and the authenticated service. Repeating the operation does not create duplicates.
- Commit attribution and display are demonstrated independently from the import anchor; unknowns remain unknown.
- A dispatch accounts for actual landed changes, even where no session record exists.
- Source transcripts, private mappings, credentials, and inventories remain outside this public repository. The checkpoint destination and exposure of imported data are checked before each real batch.

Each PR is non-draft and has a concrete verification result. Use bounded implementation subagents and an independent reviewer trailing the stack to triage findings and confirm fixes. Keep a couple of behavior-focused acceptance paths over 10-20 actual conversations/sessions, with private data outside the repository. Wait for the applicable reviews, inspect and address their findings, then merge. A successful synthetic test proves feasibility, not fleet-wide recovery.
