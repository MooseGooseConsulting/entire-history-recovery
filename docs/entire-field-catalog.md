# Entire history recovery: field and ingestion catalog

This catalog inventories the **persisted checkpoint schema and the import-relevant interfaces**, not every field in Entire's private cloud database, UI, or all CLI APIs. It is a proposed recovery contract, not a claim that a particular dataset has been imported. The primary source is [Entire CLI v0.12.2, commit c4f103d](https://github.com/entireio/cli/tree/c4f103d8dfed1f1549db9b2c793f2d7e10f65eb8). The checkpoint metadata, token, skills and redaction files and importer core are unchanged from v0.12.1; the Pi importer differs. SER10's installed CLI reported v0.12.2. Recheck against the target version before implementing.

**Status key:** **Stock** = written or derived by the current importer/writer when available; **Extend** = proposed recovery work, not currently established; **Derived** = writer or consumer-owned output; **Preserve** = keep when already present, do not fabricate; **Optional** = populate only if the original data proves it and a supported writer handles it. An empty or unknown field is not evidence that the work did not occur.

## The data model

An agent session contains one or more turns. Entire stores checkpoint metadata and session files in Git-backed checkpoint storage, separately from code commits. A code commit can name a checkpoint via `Entire-Checkpoint`; for an already-pushed commit, `linked_commits` can provide a separate attribution link. A root checkpoint may contain multiple sessions. The session-level `agent` is the **coding tool**; `model` is the model it used. Neither an imported display anchor nor a matching timestamp proves a commit was made by a session.

[Metadata and writer types](https://github.com/entireio/cli/blob/c4f103d8dfed1f1549db9b2c793f2d7e10f65eb8/api/checkpoint/metadata.go) · [import orchestration](https://github.com/entireio/cli/blob/c4f103d8dfed1f1549db9b2c793f2d7e10f65eb8/cmd/entire/cli/agentimport/agentimport.go) · [checkpoint storage design](https://github.com/entireio/cli/blob/c4f103d8dfed1f1549db9b2c793f2d7e10f65eb8/docs/architecture/ref-checkpoint-backend.md).

## Per-session checkpoint metadata

Each row names a JSON field in `checkpoint.Metadata`; `omitempty` and exact types are defined by the linked source. Source labels identify potential evidence, not a guarantee that each parser exposes it.

| Entire field | Target and expected value | Source and route |
|---|---|---|
| `cli_version` | **Derived:** actual writer version. | Entire checkpoint writer; never impersonate a version. |
| `checkpoint_id` | **Stock:** stable per-turn checkpoint identity. | Entire's deterministic importer ID derivation and existing remote-ID lookup. |
| `session_id` | **Stock:** original agent session ID. | Native session metadata; deduplicate against existing records. |
| `strategy` | **Stock:** `import` for historical imports. | Existing import writer. |
| `kind` | **Stock:** `imported`; distinguish history from live capture. | Existing import writer. |
| `created_at` | **Stock:** original turn time where present. | Native event timestamp; importer may fall back to file modification time. Record provenance privately. |
| `branch` | **Extend / Optional:** original branch only when independently evidenced. | Native metadata or contemporaneous Git evidence; stock `writeTurn` does not provide it. |
| `commit_sha` | **Stock:** import display anchor, not authorship. | Existing importer chooses a resolvable recorded commit or default-branch anchor. Keep anchor semantics explicit. |
| `agent` | **Stock:** actual originating coding agent. | Registered importer type / verified native format, not repo or date. |
| `model` | **Stock:** source model when available; empty when unknown. | Native transcript parser. Do not substitute current default model. |
| `turn_id` | **Optional:** actual correlated turn ID when present. | Native event identifier; stock `writeTurn` does not populate it. |
| `checkpoints_count` | **Stock:** importer supplies `1` per imported turn. | Importer; do not interpret as evidence of a historical commit. |
| `save_step_count` | **Preserve:** native capture bookkeeping. | Existing checkpoint or supported hook evidence; stock import does not set it. |
| `files_touched` | **Extend / Optional:** evidenced repo-relative changed paths. | Native tool events or verified commit diff; retain attribution basis. Stock importer does not pass it. |
| `is_task` | **Preserve:** legacy task-checkpoint indicator when applicable. | Existing checkpoint; subagents normally use task records below. |
| `tool_use_id` | **Optional:** native task invocation identifier when applicable. | Verified source event; do not manufacture it. |
| `transcript_identifier_at_start` | **Optional:** native message identifier at boundary. | Parser with exact native identifier; stock importer does not set it. |
| `checkpoint_transcript_start` | **Stock:** raw transcript boundary for the imported turn. | Parser's line offset; use agent-specific semantics and test round-trip. |
| `transcript_lines_at_start` | **Derived / Preserve:** deprecated compatibility field. | Existing writer/reader; do not independently invent offsets. |
| `compact_transcript_start` | **Derived:** compact transcript boundary when generated. | Entire compaction/writer; absence has legacy semantics. |
| `token_usage` | **Stock / Optional:** per-turn usage where source supplies it. | Native usage events and Entire token parser; nested fields below. |
| `redaction` | **Derived:** replacement counts and layers, never removed values. | Entire's actual redaction pipeline; unknown stays unknown. |
| `skill_events_version` | **Optional:** skill-event schema version. | Entire's supported skill extractor when explicit source signals exist. |
| `skill_events` | **Optional:** native skill invocations and anchors. | Explicit transcript or hook signals; see nested field table. Stock importer does not pass them. |
| `session_metrics` | **Optional:** duration, turn and context metrics. | Native hook or session data; do not infer totals from an incomplete transcript. |
| `summary` | **Optional:** intent, outcome, learnings, friction and open items. | Existing summary or explicitly generated downstream summary, labeled as generated. |
| `initial_attribution` | **Preserve:** opaque legacy attribution. | Existing record only; current CLI does not compute or read it. |
| `prompt_attributions` | **Preserve:** opaque legacy attribution. | Existing record only; current CLI does not compute or read it. |
| `review_skills` | **Optional:** skills actually run for a review. | Explicit review records; not guessed from topic. |
| `review_prompt` | **Optional:** actual review request. | Recorded review event/transcript, redacted through supported path. |
| `investigate_run_id` | **Optional:** existing investigation run ID. | Native investigation metadata only. |
| `investigate_topic` | **Optional:** recorded investigation topic. | Native investigation metadata only. |

## Root checkpoint metadata

These are `CheckpointSummary` fields, distinct from the per-session fields. Aggregation and path layout belong to Entire's writer, not a manually constructed JSON file.

| Entire field | Target / source |
|---|---|
| `cli_version` | **Derived:** checkpoint writer version. |
| `checkpoint_id` | **Stock:** importer's deterministic ID. |
| `strategy` | **Stock:** import strategy. |
| `branch` | **Optional:** evidenced branch via supported writer; not an assertion of main membership. |
| `commit_sha` | **Stock:** imported display anchor only. |
| `checkpoints_count` | **Derived:** writer's session/checkpoint aggregate. |
| `files_touched` | **Derived / Extend:** aggregate of evidenced session files when supported. |
| `sessions` | **Derived:** session file path entries produced by writer; nested fields below. |
| `token_usage` | **Derived:** sum with Entire's usage semantics when known. |
| `redaction` | **Derived:** aggregate counts; unknown stays unknown. |
| `linked_commits` | **Extend / verify:** explicit `{sha, repo}` associations via supported attach/link path, only for evidence-backed exact commits; server author verification applies. |
| `combined_attribution` | **Preserve:** opaque legacy value; never synthesize. |
| `has_review` | **Derived:** writer's review-kind aggregate. |
| `has_investigation` | **Derived:** writer's investigation-kind aggregate. |
| `imported` | **Derived:** writer indicates imported history. |

`sessions[]` is `SessionFilePaths`: `metadata`, `transcript`, `compact_transcript`, `content_hash`, `prompt`, and `assets_manifest`. These are writer-produced paths/hashes to stored objects, not local source paths to copy into public records. `linked_commits[]` contains `sha` and optional forge-qualified `repo`; it is distinct from `commit_sha`. Server acceptance and display must be tested on a real authorized link.

## Nested fields and associated objects

| Object | All persisted fields | Population / source |
|---|---|---|
| `token_usage` | `input_tokens`, `cache_creation_tokens`, `cache_read_tokens`, `output_tokens`, `api_call_count`, `subagent_tokens`, `subagent_tokens_complete` | Native usage events; stock parser scopes per turn. Nested subagent usage has the same structure. Missing is not zero. [Source](https://github.com/entireio/cli/blob/c4f103d8dfed1f1549db9b2c793f2d7e10f65eb8/cmd/entire/cli/agent/types/token_usage.go). |
| `redaction` | `replacements`, `layers`, `custom_rules_active`, `pii` | **Derived** from layers that actually ran, never guessed. [Source](https://github.com/entireio/cli/blob/c4f103d8dfed1f1549db9b2c793f2d7e10f65eb8/redact/counts.go). |
| `session_metrics` | `duration_ms`, `turn_count`, `context_tokens`, `context_window_size` | **Optional**, only source-backed metrics; distinguish missing from zero. |
| `summary` | `intent`, `outcome`, `learnings`, `friction`, `open_items` | **Optional**, preserve a source summary or identify generated content; nested `learnings` is `repo`, `code`, `workflow`; `code[]` is `path`, `line`, `end_line`, `finding`. |
| `skill_events[]` | `id`, `event_type`, `skill`, `source`, `turn_id`, `timestamp`, `transcript_anchor`, `native`, `collapse` | **Optional**, explicit native signal only. `skill` has `name`; `source` has `agent`, `signal`, `confidence`; `transcript_anchor` has `unit`, `start`, `end`, `entry_ids`, `tool_use_id`; `collapse` has `target`, `label`, `default_collapsed`. [Source](https://github.com/entireio/cli/blob/c4f103d8dfed1f1549db9b2c793f2d7e10f65eb8/cmd/entire/cli/agent/types/skill_events.go). |
| Subagent `task.json` | `tool_use_id`, `agent_id`, `subagent_type`, `task_description`, `files`, `token_usage`, `started_at`, `completed_at`, `transcript_unavailable_reason` | **Extend / Optional:** correlate native parent/child records with evidence, use supported `TaskPayload`; separate child transcript is a stored file. Stock `writeTurn` supplies no tasks. |
| Transcript and prompts | `full.jsonl`, optional compact `transcript.jsonl`, `prompt.txt` | **Stock:** native transcript, parsed turn prompts and redacted output through Entire. Preserve source separately; do not expose bodies in public logs. |
| Assets | Asset manifest and externalized binary objects | **Optional:** use Entire's asset handling only when supported and exposure is understood; `TranscriptAsset` writer inputs are `Name`, `MediaType`, `Data`, not JSON field claims. |

## Sources to ingestion boundaries

| Source | What it can supply | Recovery route and destination | Limits to prove |
|---|---|---|---|
| Native Codex, Claude Code, Copilot CLI, Cursor, Factory AI Droid, Pi history | Session ID, agent, messages, prompt/turn timestamps, model, usage, sometimes commit references and files | Existing `agentimport.Importer` `Discover`/`SplitTurns`, `Run`, redaction and `checkpoint.Persistent.Write`; extend discovery using a verified manifest | Current discovery is repository scoped and ordinarily 30 days; old cwd and format differ by agent. `--path` is not a repo mapping override. |
| Existing archive and cross-agent index | Locate historical sessions, machine, dates, repo hints, and sometimes preserved content | Read-only discovery and evidence-backed mapping; feed actual recoverable content to the correct agent parser | An index record is not necessarily a lossless native transcript. Verify completeness before conversion. |
| Kimi, Kilo, OMP, OpenCode and other stores | Agent-specific conversation and task information, depending on actual storage | Investigate external-agent adapters and native parsers; add tested importer adapters where needed | Installed live hook adapters are not automatically historical importers. Verify format, event boundaries, offsets and redaction. |
| Git and GitHub | Repository identity, branch history, commit SHA, commit message, diff, author, merge time and PR association | Inventory GitHub/Git separately; attach only evidenced session-to-commit links through supported Entire attachment/linking behavior | A squash commit has a new SHA. A commit alone cannot reconstruct a conversation; an import anchor does not prove authorship. |
| Existing Entire checkpoint refs | Previously persisted session records, IDs, redacted transcript, provenance and attribution | Entire checkpoint reader, remote ref discovery, search/explain | Reconcile duplicates and inaccessible refs; validate remote and server readback. |
| Agent hook events | Future lifecycle capture, live session state and commit links | Existing agent and Git hooks; audit separately | Do not replay fabricated live hook events as historical truth. |
| GitHub main/PR history for dispatch | Actual changes landed in an explicit window | If cloud dispatch cannot ingest it, a local GitHub-backed dispatch enriched with verified checkpoint information | Pagination, squash merges, permissions and partial failures; test output against GitHub. Import alone is not proven to fix cloud dispatch. |

## How to integrate

**Recommended first implementation:** use a pinned fork or carefully maintained extension of Entire's open-source importer, adding manifest-driven discovery, historical path mappings and date handling while reusing its parsers, redaction, deterministic IDs, checkpoint writer and sync behavior. Use the stock command for sessions it already selects correctly. Add formats only after native fixtures and round-trip tests prove them. Keep the user's originals read-only.

Entire CLI v0.12.2 is MIT-licensed. If code is copied or distributed, retain the upstream license and copyright notice and track the pinned upstream revision; confirm the licenses of any additional components independently.

| Option | Assessment |
|---|---|
| Extend/fork importer | Best-supported starting point: preserves known storage, parsing and redaction behavior. Requires maintaining a version-pinned patch and proving sync/server compatibility. |
| Standalone tool using Entire Go code | Possible if dependency boundaries and licenses allow; test imports carefully. Much of the CLI lives under `internal` or `cmd`, so independent reuse may require a fork/refactor instead of simple imports. |
| Write checkpoint JSON/Git refs directly | Technically conceivable but bypasses writer invariants, sanitization, IDs and compatibility; not the initial path. |
| Call cloud endpoints with custom payloads | No public stable checkpoint-ingestion endpoint has been established here. The documented raw API is internal and may change; the dispatch endpoint generates a report, not an import. Do not assume an undocumented endpoint can do this. |
| Replay hooks | Hooks model live lifecycle events; replay risks false state and commit associations. Audit hooks for forward capture, not as a fake historical importer. |

The attribution stage needs its own proof: resolve evidence to exact commit(s), invoke or extend supported trailerless attachment behavior, test the server's identity checks, and check the result in `checkpoint explain`, search, activity and dispatch. A PR squash and an unlinked session must be explicit test cases. Never rewrite shared history merely to attach metadata.

## Verification and remaining boundaries

1. Extract the v0.12.2 structs in CI or a schema check and compare this catalog against the actual JSON tags; fail or flag drift on upgrades.
2. In an isolated repository, import a synthetic older session with a deleted original checkout and distinct source/destination paths; verify correct repo, agent, model, timestamps, redaction, offsets and duplicate-free rerun.
3. Test one source-backed commit association, an unlinked session, a multi-agent checkpoint and a squash merge. Check local reader, remote checkpoint refs, authenticated server, fresh clone and activity independently.
4. Compare a five-repository dispatch to paginated GitHub history for the same window; missing checkpoint or access failure must not become a no-commit claim.
5. Keep private evidence, manifests and raw transcripts outside a public repository. This catalog describes intended sources and destinations; no real session content or credentials belong here.

Cloud-only structures and undocumented server ingestion behavior remain outside this catalog until directly observed or documented; do not claim completeness for Entire's private internals.
