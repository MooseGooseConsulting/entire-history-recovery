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

The full render displays no recorded intent prompt. Source inspection is still
needed to distinguish unavailable initiating content from an import/rendering
gap; do not invent a prompt to fill that field. This round trip proves that
recovered content can reach and be read through Entire's service. It does not
establish a fresh-clone retry, complete global duplicate prevention, complete
native turn semantics, browser UI rendering, commit attribution or dispatch
correctness. Those remain separate acceptance work.

Existing-repository checks also found a real [Git/API visibility discrepancy](server-inventory-boundary.md).
Publication success must therefore continue to require known-ID API readback,
not just a successful Git push.
