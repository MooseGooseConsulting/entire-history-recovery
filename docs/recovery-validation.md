# Recovery validation

## Snapshot integrity

The recovery wrapper validates every nonblank JSONL record before handing the
source to Entire. A valid record has a type and an object payload. The first
record must identify the session; a later session header is rejected as a
concatenated source. Unknown event types are retained for forward compatibility.

This addresses a reproduced failure in the prototype: Entire's Codex parser
skipped malformed records and still reported readable prompts as importable.
Six damaged-source tests failed before the change and pass after it. Both
preview and write paths reject these sources without creating checkpoint refs.
This is rejection, not automatic repair: truncated sources require a separately
preserved and explicitly repaired copy before ingestion.

A SHA-256 digest also binds the bytes validated by the wrapper to the bytes
Entire subsequently reads. A change between those reads aborts parsing. It does
not lock a live source or prove that an agent has finished writing it; select an
inactive recovered snapshot.

## Executed coverage on SER10

| Case | Checked result |
| --- | --- |
| Truncated tail, malformed middle, null/array record, missing type, second session header | Rejected before checkpoint writes; original source unchanged |
| File changes after validation | Rejected before parsing |
| Dry run | Reports candidate count without checkpoint refs |
| CRLF and unknown event types | Accepted without rewriting source bytes |
| Appending an identical second prompt | Only the new line gets a checkpoint; repeating skips both, for git-branch and git-refs |
| Historical source and deleted original cwd | Imported into an isolated destination |
| Isolated Git remote and fresh clone | Stored checkpoint readable with both backends |
| Native task/context event ordering | Model association requires matching unambiguous task IDs |
| Private historical session | 14 importer units stored locally; 9 native completed user-message text matches; repeat imports skipped |

The private-session counts do not establish 14 human prompts or complete semantic
turn recovery. Native user-message events can also include injected instructions.

## Still required

- Remote-only duplicate detection before importing into a clone that has not
  fetched checkpoint refs.
- Exact native turn semantics, including injected context and aborted tasks.
- Production sync and authenticated Entire server readback of the real session.
- Evidence-backed commit attribution and actual dispatch behavior.

The current prototype is local-only. These tests do not establish server ingestion
or authorize a bulk import. Private source data and authentication material stay
outside the public repository.
