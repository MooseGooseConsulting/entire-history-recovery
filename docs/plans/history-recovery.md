# Historical session recovery: implementation and rollout plan

Updated 2026-10-29. **This is the authoritative execution plan.** [GOALS.md](../../GOALS.md) states the outcome; the [field catalog](../entire-field-catalog.md) defines the target data. This plan supersedes the sequencing in earlier handoffs. Status: reviewed against current code, implementation incomplete; no bulk-ingestion readiness claim.

## Outcome and decisions

Recover as much genuine agent history as available sources support into the correct Entire repository, even when the original computer or Git checkout is gone. Recognize history already in Entire, import only missing material, and verify server-visible results. Separately make dispatch reflect actual landed commits.

User decisions from the October 29 conversation:

- Build and prove ingestion before searching all machines. Do not substitute repeated stock `entire import` calls for recovery tooling.
- Missing original checkouts are a required case. A replacement clone is a destination, not a prerequisite for finding a source.
- Use Windows now, where Entire is authenticated; SER10 remains a useful Linux execution/test host. Neither host's auth failure blocks independent work on the other.
- Keep tools, synthetic fixtures and sanitized docs in this public repo; real transcripts, manifests and credentials remain private.
- Deliver stacked **non-draft** PRs. Delegate bounded implementation tasks to subagents and have an independent trailing reviewer triage automated and subagent findings against the actual code. Fix valid findings, verify the fixes and inspect required checks before merging. Do not merge just because a PR was created.
- Use a small number of behavior-focused tests over 10-20 actual conversations/sessions. Keep the private cohort fixed and test the externally observable recovery result, including actual write retries; do not inflate coverage with many tests that repeat implementation details.
- Target the field catalog and review the complete path before bulk writes.

Technical choices for this revision:

| Question | Choice and evidence | What changes this choice |
| --- | --- | --- |
| Who owns recovery? | This tool owns discovery, mapping, normalization, duplicate decisions and receipts. `fixedSource.Discover` already bypasses stock age/cwd filters. | No change: relying on current checkouts violates the requirement. |
| How do we write Entire records? | Reuse pinned v0.12.2 parsers/redaction/writer behind an adapter where they fit. `ImportCodex` already writes readable local checkpoints. | A field or source cannot be represented correctly, or an upstream error cannot be made safe: use a narrowly pinned patch/fork or lower-level supported writer. Prove it through the same reader/server tests. |
| Can we post arbitrary payloads to the cloud? | No stable cloud ingestion endpoint has been established. First prove checkpoint transport and server indexing. | A documented or source-verified endpoint with tested schema/auth/retry semantics is found. Do not guess one. |
| Does a commit reconstruct a conversation? | No. Git evidence can supply change history and attribution candidates, not missing dialogue. | A genuine recoverable transcript/export supplies the dialogue. |
| Initial supported scope | One Codex format and one mapped real repository, then a bounded native-session pilot. | Expand only when a concrete recovered source requires another adapter. |

## Verified starting point and known gaps

Initial code baseline: `5b2a0f6`, Entire Go dependency v0.12.2. The implementation stack now also includes existing-coverage assessment, a private cohort test and the sealed-snapshot CLI; these remain open PRs. Installed Windows CLI previously reported v0.12.1; verify both versions for every compatibility result.

| Evidence | What it establishes | What it does not establish |
| --- | --- | --- |
| `TestImportOldCodexSessionIntoDifferentRepository` | A 2020 transcript with `/deleted/checkout` can import into another isolated repo. | Correct mapping of a particular orphaned source to a real repo. |
| Standalone local Windows run | Real source produced 14 checkpoint records; installed Entire read back 14; retry imported zero and skipped 14; source hash unchanged. | Fourteen human prompts, server indexing, correct original commit attribution. |
| Private local test | 14 importer units, 9 native completed user-message text matches; model found for 14 units. | Full native turn semantics; native user events may include injected instructions. |
| Linux suite and race run | Covered local writes, source integrity, IDs, transport to a bare Git remote and fresh-clone readback. | Entire's production sync queue or cloud behavior. |
| Windows test suite | Functional assertions reached success, but several temporary-directory cleanup operations failed with open-handle errors. | A green Windows suite. Find and fix resource ownership or use a deliberate process-lifetime boundary with a cleanup acceptance test. Do not suppress the failures. |
| Local authenticated Entire activity | This computer can reach the service. | A recovered checkpoint has arrived there. |
| Private 12-session cohort | 37 locally written checkpoint records; real second write imports zero and skips 37; readback matches selected identities, stored transcript digests and boundaries. Source hashes and refs remain unchanged on retry. | Correct destination mapping, reconciliation with existing production/live records, or server visibility. |
| Sealed CLI over the same 12 actual sessions | Snapshot, preview, local write and real write retry succeed as separate Windows processes. Zero leftover staging files; parent can remove the temporary destination after process exit. | A fix for the underlying in-process Windows handle lifetime, or a production batch runner. |
| Linux at `f50a377` | Full synthetic suite and vet pass, including the snapshot package. | Linux execution of the private Windows cohort; it was not transferred. |

Before `agentimport.Run`, `AssessCodexStorage` performs a separate strict inventory of local checkpoint refs and every configured Git checkpoint read target. It avoids upstream `List`'s best-effort omissions by enumerating each storage layout and validating summaries/session metadata directly; remote object hydration happens in a temporary bare repository. `agentimport.Run` itself still receives neither `ReadRemotes` nor `RemoteRefLister`, so its internal ID check is unchanged. The strict preflight can skip Git-visible exact coverage or hold failures/conflicts, but it cannot establish Entire service ingestion/index completeness or authorize a global-absence claim.

The experimental [`cmd/recover-one` CLI](../snapshot-command.md) is in the open implementation stack. It accepts verified private snapshots and previews by default. The local write path checks strict local/configured-Git exact coverage before import, but does not enforce production repository mapping, service/server inventory completeness or semantic reconciliation. Zero-write retries against Git refs are not proof of global duplicate prevention.

## Recovery without the old checkout

Proposed flow: selected source -> immutable private snapshot -> format adapter -> normalized session with provenance -> verified destination mapping -> existing-coverage reconciliation -> staged Entire records -> scoped sync -> authenticated server readback -> receipt.

| Available evidence | Action | Required behavior |
| --- | --- | --- |
| Native transcript; original checkout deleted | Parse directly from the snapshot. Resolve historical repo identity, make an isolated replacement clone, import there. | Neither current cwd nor file modification age controls eligibility. |
| Moved/renamed repo, fork, worktree or several machines | Resolve forge repository identity and an explicit historical-alias mapping; distinguish forks and shared worktree roots. | Same folder name or owner is not sufficient. Keep code-repo identity separate from checkpoint-storage destination. |
| Export, archive or index with real message bodies | Build an adapter for that exact source shape. Retain provenance and fidelity; do not present an export as an original native transcript. | Preserve available roles/times/tool outputs; do not fabricate absent native events or metrics. Prove Entire rendering before widening this source class. |
| Index containing only titles, paths or session IDs | Use it to locate surviving bodies or match existing Entire records. | Metadata alone is not an ingested conversation. |
| Existing Entire checkpoint survives but local transcript does not | Recover and reconcile through Entire's reader/storage. | Do not reimport an already present checkpoint as a new session. |
| Only Git/PR history survives | Recover commit/change evidence for reporting and possible corroboration. | No synthetic dialogue, model, token counts or session IDs inferred from authorship. |
| Source spans multiple repositories | Partition only where event evidence establishes ownership, retaining cross-repo provenance privately. | Do not copy the full multi-repo transcript into every repo. Hold publication if safe partitioning cannot be established. |
| Repository cannot be resolved or no authorized destination exists | Retain a private unresolved manifest entry and continue other items. | No arbitrary fallback into the public tools repo. |

Mapping evidence order: recorded forge remote/repository identity; corroborated Git objects and historical mapping; archived cwd plus verified machine/path mapping. Normalize Windows/Unix path spellings for lookup, never treat the normalized path as identity. Resolve GitHub renames to a stable repository identifier. Preserve evidence references privately. Conflicting evidence produces a held item, not a guessed destination.

Missing source content remains an explicit recovery limit, but discovery must check the selected archive/export/Entire sources before classifying it unavailable. Do not make the user recreate vanished checkouts.

### Concrete source and destination contract

The snapshot command now reads a source explicitly asserted inactive, copies it into a private temporary file, hashes while copying, validates every record, verifies original stat/digest stability, and atomically publishes a content-addressed read-only file using a no-overwrite hard link. Existing snapshot paths are verified rather than overwritten. Apply verifies the digest and pins it through the importer's reread. `--inactive` is an operator assertion, not process detection or a lock. A changing source detected during intake, interrupted copy or unvalidated record yields no accepted snapshot. Reading a changing file twice is not a substitute for a consistent export; if an active source cannot be locked/exported consistently, defer it. Keep original bytes and newline conventions.

The mapping manifest and preview must name `code_repository_id`, `code_repository_url`, `destination_checkout`, `checkpoint_write_remote`, `checkpoint_write_url`, `checkpoint_read_remotes`, `snapshot_sha256`, `mapping_revision`, `display_anchor_sha`, and `display_anchor_basis`. These are proposed private tool fields, not claims about Entire's persisted schema. Resolve inherited settings and remote election, compare their result with the manifest, and reject disagreement or ambiguity before writing. Display both repository visibility and checkpoint-storage visibility; allow a different storage repository only when that exact destination is configured and authorized for this code repository. Put these resolved values in the operation receipt.

For a source with no provable commit, an existing commit in the replacement destination may serve as the required display anchor: select and record the default-branch HEAD at planning time, label the basis `destination-default-head-display-only`, and freeze its full SHA for the operation. This is not a claim that the historical work produced that commit. Test that no `linked_commits` or authorship association is fabricated, and inspect how the service presents it. If the service cannot distinguish it from true attribution, hold publication and extend the writer/display path instead. If the destination has no existing commit, do not invent a code commit to satisfy the importer; that source needs an anchorless writer path proven separately or remains held.

The missing-checkout integration fixture must supply only a recovered transcript and mapping evidence, assert the original path is absent on the executing host, resolve a stable repo identity, and import into a separate replacement clone. Assert the old path remains absent and no read/open operation targets it (instrument the source/mapping filesystem boundary). Do not rename, delete or conceal a user's working checkout to create this test. A Windows-recorded source on Linux is an additional real-path case, not a replacement for the isolated assertion.

## Deduplication contract: more than deterministic IDs

Deduplication is **not yet proven complete**. The production contract covers three different things: repeated source files, repeated semantic session coverage, and repeated writes after retries. None can be replaced by the others.

### 1. Inventory existing coverage before deciding what is new

For the selected destination, record a complete-enough inventory of local storage, configured checkpoint read remotes (including a separate checkpoint repository), both legacy metadata branches and per-checkpoint refs, and service-visible records. Read every session within each relevant checkpoint, not just session index zero. Resolve the elected write destination independently of the read-candidate chain.

Use `checkpoint.Open`, `Persistent.List`, `Read` and `ReadSessionContent` plus the appropriate fetch/list adapters. Enumerate remote-only checkpoints from a fresh clone; fetch metadata/bodies necessary for comparisons without advancing unrelated refs. Do not assume a default clone fetches custom checkpoint refs. Check every page/cursor and distinguish a verified empty result from an auth, timeout, parser or missing-object failure.

Service search/activity lists are discovery aids, not proof of absence. First establish an exhaustive supported listing or reconcile the server's storage inventory with its ingestion status. Inventory scope may be the selected repository/agent/native-session identity if the service proves that query is exhaustive for that identity; it need not always scan the entire account. A search result cap is not such a guarantee. If neither a complete scoped service query nor authoritative storage-to-service coverage can be established, hold all new recovery writes for that repository, not just candidates where a possible duplicate happened to be found. Do not claim global duplicate prevention. A pushed-but-not-yet-indexed checkpoint remains pending, not missing.

Inventory failure stops writes for the affected repository. Recheck remote state immediately before publishing. No partial list becomes an empty database. The upstream fail-open listing behavior must be guarded or patched before production use.

### 2. Compare session and event coverage

| Comparison | Decision |
| --- | --- |
| Identical snapshot SHA-256, even at a different path | Reuse the snapshot record; still check destination completion. A file hash alone does not prove a successful server import. |
| Same forge repo identity + agent + original session ID | Candidate same session. Compare provenance and actual event coverage; do not skip all of it on ID equality alone. |
| Same native event/turn IDs and compatible content | Already covered units are skipped, including live-hook checkpoints with different checkpoint IDs. |
| Session has no native stable event IDs | Use a versioned adapter-specific event sequence/coverage mapping. Reconcile against old raw-line-based import IDs and redacted representations. Do not silently replace identity with filenames or timestamps. |
| Same session ID but conflicting non-overlapping provenance or incompatible bodies | Hold as collision/conflict; never overwrite the existing session on ID equality. |
| Partial capture, resumed session, compaction or longer backup | Identify exactly which events are covered. Import only demonstrably absent units; preserve existing attribution and metadata. Uncertain overlap is held. |
| Same prompt text in two real turns | Preserve both unless native identity and surrounding sequence prove they are the same occurrence. Prompt hashes alone must not collapse repeated requests. |
| Same conversation exported to two formats | Cross-format fingerprints suggest candidates; exact event alignment/provenance must establish equality before skipping. |
| Existing native/live record is richer | Keep it. Do not replace it with a poorer imported record or erase task/commit metadata. |

Content fingerprints are private comparison aids. Raw-source hashes and Entire's redacted/stored content hashes have different meanings. Version normalization, redaction and identity algorithms; support previous versions during migration. Multiple checkpoints can legitimately contain the same full transcript while selecting different spans, so full-transcript equality alone does not make the checkpoints duplicates.

Choose and freeze the persistent event identity policy after native-format tests, before the real write. Prefer native IDs; otherwise persist verified alignment and old-to-new mappings in the ledger. Do not regenerate IDs merely because newlines, paths, export ordering or the adapter version changed. Preserve original bytes to avoid changing raw offsets accidentally.

For partial coverage, append new imported checkpoints selecting only demonstrably uncovered event spans; preserve the original session identity and retain context as transcript context, not newly counted prompts/tokens. Existing checkpoint refs, their metadata, and attribution must stay byte-identical. Full transcripts may be retained when the supported writer requires them, but offsets and usage must expose only the uncovered coverage. Never merge into or replace an existing checkpoint in this initial rollout. If an intended new span collides with an existing checkpoint ID, or the supported writer cannot encode that span without re-counting covered work, hold the item and implement a tested writer extension before retrying. The test compares all pre-existing ref IDs and metadata before/after as well as the new coverage. Enrichment of an existing record is a separate future mutation contract.

### 3. Make retries and concurrent work safe

Implement a private transactional SQLite ledger. Proposed tables: sources/snapshots, destination mappings, source events, existing checkpoint coverage, import operations, and verification receipts. Keys include stable repository identity, agent, source session identity, event identity and adapter version; version changes require reconciliation rather than creating a second operation automatically.

Each operation progresses through discovered -> mapped -> reconciled -> staged -> published -> server-verified, with held/failed outcomes and retry reasons. Persist intended IDs and source/target digests before a write; never mark completion merely because Git push exited zero. On restart, reconcile observed state before retrying a side effect. Test crashes after each transition, including push succeeded but receipt was not saved.

One elected batch writer per destination, with an OS/process lock and durable ledger ownership. Independent hosts must not publish concurrently using separate ledgers; use a single coordinator and shared authoritative ledger before introducing parallel workers. Existing live agent capture can still race: refresh coverage, use non-force ref updates/compare-and-swap where available, and stop/reconcile if a ref changed. Never overwrite a divergent existing checkpoint ref. Deterministic IDs help retries of our own imports but do not by themselves solve native-live duplication.

## Field preservation and source fidelity

Keep the [full field catalog](../entire-field-catalog.md) as the schema inventory. Add an executable per-field coverage report with target, source evidence, local readback, server readback, and reason for absence. No blanket claim that all fields are supported.

| Field family | Implementation obligation and acceptance |
| --- | --- |
| Session, agent, event identity and source kind | Preserve actual tool/session IDs; distinguish imported history from live capture and export reconstruction. Test collisions and original checkout absence. |
| Prompts, responses, tools and transcript boundaries | Parse native lifecycle rather than all user-role strings; preserve injected context without counting it as human requests. Include repeated prompts, aborted turns, resumed tasks, context before prompts, compaction, missing tail and unknown events. |
| Model, times and usage | Source-backed per-event values; test model switches, time zones, absent timestamps, cumulative counter resets and subagent double-counting. Missing is not zero; snapshot mtime is not historical event time. |
| Branch, touched files, commits and links | Evidence-backed values only. Explicit display anchor remains distinct from `linked_commits` and true session-to-commit attribution. Test squash/rebase/merge cases separately. |
| Tasks, skills, assets and summaries | Preserve supported native parent/child IDs and payloads; test missing child transcript and attachment exposure. Generated summaries are labeled and not prerequisites for core recovery. |
| Redaction, storage hashes and writer bookkeeping | Reuse real sanitizer/redactor, validate counts and offsets, preserve hashes in their correct domain. Process-global redaction configuration requires process isolation per destination until safe reconfiguration is proven. |

The first pilot supports the required core fields and explicitly reports other families as preserved, unsupported or unavailable. Do not delay a valid core-session pilot for optional summaries, but do not call the full catalog complete until every row has an implemented disposition and evidence.

## Ordered implementation slices and review gates

These are capability slices, not existing PR numbers. Current PR #2 contains goals and #3 the local prototype. Create subsequent PRs stacked on their actual dependency branch; rebase/retarget after parent merges and rerun affected checks. Never infer readiness from a bot job being skipped.

| Slice | Actions and design questions | Acceptance before proceeding |
| --- | --- | --- |
| A. Reliable one-source command | Promote `cmd/recover-one`; fix Windows handle lifetime; require immutable snapshot, explicit mapping/anchor, preview then apply. Keep current synthetic old/deleted-path test. Confirm parser doesn't mislabel injected context. | Linux and Windows checks pass, or an explicitly supported process-isolated CLI test proves handles released on exit; wrong mapping/damaged source fails before writes; field report explains every emitted core value. |
| B. Existing-history reconciliation | Build scoped destination inventory, session/event coverage, ledger and legacy/live ID reconciliation. Guard list failures. Implement only the selected source format first. | Fresh clone sees remote-only existing records; preexisting live capture is not duplicated; partial sessions add only missing coverage; unavailable inventory writes nothing. Unsupported service-only cases must pass a HOLD/no-write test; this tests safe refusal, not successful reconciliation. |
| C. One real server round trip | Establish the complete scoped inventory/coverage contract for the selected real destination first. Reconfirm repo identity/privacy and source mapping; freeze expected local IDs/content; stage locally; push only intended checkpoint changes through the supported transport; poll authenticated server readback with bounded timeout. Use the existing Windows login. | Correct repository/agent/session/timestamps/body representation visible via Entire API-backed explain or reader; fresh clone verifies same data; repeat from a second fresh clone with no prefetched custom refs creates zero records through discovery/reconciliation. Timeout remains pending with no blind reimport. C does not pass if its real source merely stays held. |
| D. Small resumable pilot | Build a manifest from a bounded selected set (target 10-20 sessions, fewer if only fewer valid inputs exist), including another machine/path, preexisting capture and a partial/resumed session. Add interrupted retry and concurrent-state tests. | Every item has an accounted outcome; zero unexplained duplicates/mapping errors; source hashes unchanged; all published items server-verified; stop/resume reproduces the same result. Measure throughput/storage. |
| E. Scale the proven source class | Only now enumerate approved archives/indexes/machines read-only. Stream/bound parser memory, use ledger-based resume, rate limits, backoff and bounded repository concurrency. | Pilot metrics set batch size/timeouts; per-batch reconciliation and review complete; no growth in duplicates or unverified writes. Unsupported formats are held, not silently dropped. |
| F. Add alternative source adapters | Choose one actual export/store that unlocks missing history; build normalized representation and a pinned writer extension if stock parser cannot accept it. | One real source of that class passes A-D's fidelity, dedup and server gates before its bulk ingestion starts. This may run while proven native classes ingest. |
| G. Attribution and accurate dispatch | Prove trailerless source-backed linking, including squash and no-link cases. Compare dispatch to paginated GitHub default-branch history in the exact same time window. | Actual landed commits appear regardless of checkpoint coverage. If cloud dispatch cannot satisfy this, implement the planned GitHub-backed report enriched by verified Entire records. |

Core-session bulk ingestion begins **after A-D**, not after every optional enrichment or new adapter. Session recovery can proceed without fabricated commit links; full project completion still requires G and field-by-field disposition. Broad history discovery is not a substitute for A-C.

The remote-only duplicate fixture has three checkouts: writer publishes the first import; reader verifies fetched content; a separate retry clone starts with only the normal code-branch fetch and no custom checkpoint refs or recovery ledger. It resolves configured read remotes, enumerates them, loads necessary metadata, and reconciles before apply. Test both storage backends and a separate checkpoint remote. Require zero new checkpoint IDs and zero changed existing refs. This distinguishes actual remote discovery from the already-passing local-ref dedup test.

## Required adversarial scenarios

| Scenario | Expected result |
| --- | --- |
| Original checkout gone, transcript moved, old timestamp | Successful mapped recovery without recreating old path. |
| Repo renamed; same basename in another owner/fork | Correct stable identity; conflicting destination held. |
| Existing imported record only on remote; empty fresh clone | Skip matching coverage after inventory, not after accidentally writing it. |
| Existing live-hook checkpoint with unrelated checkpoint ID | Find session/event overlap and create no duplicate. |
| Both checkpoint storage formats, separate checkpoint remote, multiple sessions in one checkpoint | Inventory all relevant session coverage and preserve remote selection policy. |
| Server has records absent from accessible Git refs | Detect/reconcile through a complete service inventory or hold; cannot claim absence. |
| Permission denied, partial pagination, timeout, missing object | Stop affected writes; distinguish from empty history. |
| Duplicate backups, exports with CRLF/reordering, repeated text, IDs reused | Deduplicate true copies; preserve real repeated events; conflicts held. |
| Existing prefix and new suffix; compacted/resumed source | Only proven missing coverage imported; no silent full replacement. |
| Crash before/after write, before/after push, before receipt | Resume without a duplicate or false completion. |
| Concurrent import/live capture or changed remote ref | Serialize our writers; reject conflicting update and reconcile coverage. |
| Old parser, redaction or ID algorithm changes | Reconcile prior mappings; no automatic second identity for old events. |
| Redaction canaries, assets, source integrity, multi-repo content | Synthetic secrets removed from all published bodies/prompts; source unchanged; unrelated private content not exposed. |
| Server lag/rejection, actor attribution mismatch | Pending/failed receipt with diagnostic; no repeated blind publication. |
| Windows cleanup and long-running batch | No unbounded handle or memory growth; process exit releases temporary repos. |

## Execution locations, code seams and commands

Use a dedicated recovery checkout on the chosen Windows or Linux host. Unrelated repositories are not recovery staging areas. Private source pointers and evidence stay outside the public repo; do not add their paths or contents to fixtures or CI.

Existing seams: `internal/recovery/one.go` (`ImportCodex`, `fixedSource`, `validateCodexSnapshot`); `one_test.go`; `integrity_test.go`; `prompt_evidence.go`. Proposed modules: `internal/source` for snapshot/format adapters, `internal/mapping` for repository evidence, `internal/reconcile` for existing coverage, `internal/ledger` for transactions, and `internal/transport` for publish/readback receipts. Create each only in the slice that uses it. Keep the command a thin wrapper, use argument arrays rather than shell-built commands.

Upstream reference: module `github.com/entireio/cli@v0.12.2`, `cmd/entire/cli/agentimport/{agentimport,codex}.go`, `checkpoint/open.go`, `strategy/checkpoint_read_remotes.go` and `git_operations.go`. Inspect their current signatures before implementing. `Run`'s `ReadRemotes`/`RemoteRefLister` provide discovery hooks; they do not implement semantic live-session deduplication for us.

Existing verification commands (no bulk writes):

```powershell
# Windows, from the development checkout; use installed Go explicitly.
mise exec go@1.27.3 -- go test ./... -count=1
mise exec go@1.27.3 -- go vet ./...
# Optional private session test: set RECOVERY_TEST_CODEX_SOURCE privately first.
mise exec go@1.27.3 -- go test ./... -run TestPrivateHistoricalSession -count=1 -v
```

```sh
# Linux, from the development checkout.
GOTOOLCHAIN=go1.27.3 go test ./... -count=1
GOTOOLCHAIN=go1.27.3 go test -race ./... -count=1
GOTOOLCHAIN=go1.27.3 go vet ./...
```

For server verification, first inspect `entire agent-help checkpoint explain`. Use `--repo` and the full checkpoint ID to force API-backed cross-repository readback, rather than accepting a local cached record as cloud proof. Keep metadata/transcript responses private; compare bodies/digests in the verifier without printing them. Confirm the server returns all sessions/assets required by the tested record.

## Security, publication and recovery

Before every apply: verify source digest, mapping revision, destination identity, visibility, elected checkpoint remote, redaction settings and inventory completeness. A private source must not accidentally follow an inherited public checkpoint remote. Report intended changes without raw transcript bodies. Preview is the default; the user's authorization covers this scoped recovery work, not unrelated infrastructure changes.

Store original snapshots, source-derived fingerprints, mappings and ledger under restricted private storage. Use synthetic data in tests and public PRs. Test redaction with known canaries across prompts, JSONL, tool outputs and assets; disabling redaction is not a repair strategy. Keep generated logs free of tokens and private bodies.

Prepare an operation receipt with before/after ref IDs, newly created checkpoint IDs, source digest, writer version, mapping evidence reference and verification state. On conflict stop that operation. Do not force-push shared history or delete preexisting records to make a test pass. Recovery of erroneous publication uses an individually reviewed, supported checkpoint deletion/correction after establishing whether newer work depends on it; do not pretend a Git ref rollback removes all cloud copies. Preserve the private audit receipt. Draft and verify the exact corrective action before requesting any additional permission it actually needs.

## Review, estimates and completion

At each implementation slice: self-review against this plan; external review of the actual PR head; address material findings; run relevant checks; verify required GitHub rules; merge in dependency order. The active inherited organization ruleset was read on October 29: it requires a PR, resolved review threads and linear history, and prohibits deletion/non-fast-forward updates on the default branch. It names no required status-check contexts and requires zero formal approving reviews; this does not waive the user's requirement to complete actual review and validation before merging. A 404 from the legacy branch-protection endpoint does not mean no rules apply. Recheck applicable rules before each merge, and do not bypass them. A skipped review job is not an approving review. Plan review is not implementation approval or runtime proof.

Planning estimates, not delivery promises (one engineer-equivalent of focused work, excluding external review/auth/service waits):

| Milestone | Initial estimate from this baseline | Re-estimation trigger |
| --- | --- | --- |
| A-C: trustworthy one-session server proof plus existing-history reconciliation | 2-4 working days | First real inventory exposes missing server coverage, or writer requires a fork. |
| D and readiness to start bulk for the proven native source class | Another 1-3 working days; approximately 3-7 total | Pilot measures duplication, parsing, index lag and transport cost. |
| Wider recovered formats and final dispatch/attribution completion | Several further days to weeks; no reliable completion date before pilot/source sample | Number and fidelity of surviving formats; required writer extensions and attribution behavior. |

Re-estimate immediately after C and D with measured sessions/minute, bytes/session, checkpoint expansion, remote round trips, indexing delay and error/retry rate. Bulk runtime is candidate count divided by measured verified throughput plus retries/held-item work; candidate count is currently unknown. Do not promise a calendar completion date or a recovery percentage before those measurements.

Done for a source class means its selected recoverable sources have verified server records or a specific accounted held/unavailable outcome, repeated and interrupted runs add no duplicate coverage, and all required fields have a recorded disposition. Done for the project additionally means evidence-backed attribution where possible, the dispatch acceptance case passes, reviewed changes are merged, and operational docs/receipts allow another operator to resume safely.

Keep [history-recovery.notes.md](history-recovery.notes.md) current with deviations, review findings and the actual stopping state. Public notes contain no private inventories. No open implementation gate is marked passed by writing this plan.
