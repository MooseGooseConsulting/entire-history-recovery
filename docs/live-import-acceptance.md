# Live import acceptance

Verified on 2026-10-29 with installed Entire CLI 0.12.1 and the recovery tool's
pinned Entire v0.12.2 writer. Real sources, checkpoint identifiers, transcripts,
digests and raw command output are retained privately.

## Retained local cohort

Twelve actual inactive, single-header Codex sessions produced 37 local checkpoint
records. The native Entire CLI listed 0 before the first write, 37 afterward,
and 37 after repeating all 12 imports. Native detail reads covered the expected
12 session identities; transcript exports and a full conversation render were
inspected. The destination was retained and has no Git remotes.

This is local repeat-import evidence. It does not test whether an empty ordinary
clone can discover already-existing remote history.

With prompt normalization and the process-boundary changes, a later run used
two distinct private cohorts: 19 additional sessions with human and assistant
messages, plus 12 retained sealed snapshots from the original cohort. The 31
sessions produced 69 local checkpoint records and both cohorts added zero on a
real retry. Four original source paths from the earlier cohort were no longer
available; the 12 retained snapshots remained usable and unchanged. These are
counts of checkpoint records, not necessarily human turns.

For the 19 human-conversation sessions, the installed Entire CLI's `--full`
view was exercised against a checkpoint for each session. Each view contained a
bounded fragment of a stored redacted human prompt and a bounded fragment of a
stored assistant response. Stored sanitized transcript hashes, session IDs,
turn boundaries, model values and source hashes were checked separately. This
is a useful local rendering check, not proof that every character of every
message is displayed or that these 19 sessions reached the hosted service.
The opt-in test uses `RECOVERY_TEST_NATIVE_RENDER=1` alongside the private
`RECOVERY_TEST_CODEX_MANIFEST`; neither manifest nor transcripts belong here.

## One real server round trip

A selected audit session was mapped to its private audit repository using its
native recorded working directory, repository URL, consistent turn directories
and a commit available both locally and on GitHub. The commit corroborates the
destination/display anchor; it is not proof of authorship.

The repository was available for onboarding and had no existing Entire mirror.
After creating its private mirror, authenticated inventories of all GitHub and
Entire refs contained no checkpoint storage refs. The fresh replacement clone
had only its intended origin, no alternate checkpoint destination or hooks, and
an empty native checkpoint list.

Preview and local write each reported one session and one checkpoint. Local
readback matched session identity and model. The only added ref was the
checkpoint-storage branch. Both remote inventories remained unchanged during
staging. Immediately before publication, the destination's stable Entire ID,
private visibility, readiness, sealed source digest and empty remote checkpoint
namespace were rechecked. Only the checkpoint-storage branch was pushed; its
remote object hash matched the staged object.

API-backed `entire checkpoint explain --repo ... --json` then returned the
expected session, Codex agent, imported kind and source-backed model.
`--transcript` returned 1,415,968 bytes whose SHA-256 exactly matched the local
stored transcript. The original source hash remained unchanged. `--full`
rendered the assistant's actual audit results through the service-backed reader.

In a later authenticated check, Entire search returned the imported checkpoint
as a session hit with the expected repository, Codex agent and session ID.
However, the repository checkpoint-list API returned an empty array for the
same repository while reporting `history_complete: true`. Thus search and
known-ID readback can discover this record, while the repository list does not;
do not claim that all Entire views display imported records correctly.

The full render displays no recorded intent prompt. Source inspection established
that this subagent's sole user-role input was an injected environment-context
block, with no human request following it. Entire's rendering intentionally
filters that block; the original transcript and context are preserved. The
parent-session task has not been attributed to this subagent. This round trip proves that
recovered content can reach and be read through Entire's service. It does not
establish complete global duplicate prevention, complete
native turn semantics, browser UI rendering, commit attribution or dispatch
correctness. Those remain separate acceptance work.

The same sealed source was subsequently retried on SER10 from a separate
single-branch clone with no local checkpoint refs. After repository identity and
the historical anchor were verified, both preview and a real write attempt
reported one session, zero imported and one skipped. Local and remote checkpoint
refs were unchanged. An earlier attempt against a mistakenly selected clone was
rejected before writing and is not counted as a test of this destination.

Existing-repository checks also found a real [Git/API visibility discrepancy](server-inventory-boundary.md).
Publication success must therefore continue to require known-ID API readback,
not just a successful Git push.
