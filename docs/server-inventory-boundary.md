# Server inventory boundary

Source inspection of the pinned Entire v0.12.2 client found authenticated
checkpoint detail/transcript reads by known checkpoint ID, but not an exhaustive
native-session-ID inventory in that client. This is a client capability finding,
not proof that the server has no other endpoint.

- `cmd/entire/cli/checkpoint_api_reader.go`: the reader loads known checkpoint
  details; its `List` method rejects enumeration and points callers to search.
- `cmd/entire/cli/search/search.go`: search supports paging and reports partial,
  truncated, incomplete-coverage and lower-bound flags. Its client configuration
  has no exact native-session-ID enumeration parameter. Exhausting a search query
  is not an authoritative proof that a session does not exist.
- `checkpoint.Persistent.List` inventories checkpoint storage refs; complete
  configured-remote enumeration and hydration must be implemented separately.

The [rollout plan](plans/history-recovery.md) therefore still requires either a
complete scoped service inventory or a verified authoritative relationship
between storage refs and service-visible checkpoints. Local exact-coverage
assessment is not sufficient to publish recovered records without that gate.
Known-ID authenticated readback can still prove that a particular published
checkpoint arrived; it cannot prove that no other checkpoint covers the session.

Next investigation: trace the repository's actual checkpoint storage and mirror
configuration, verify its remote ref inventory, and inspect supported service
listing capabilities. Do not turn this finding into an indefinite requirement
for a hypothetical API; test the storage-authority alternative as well. Until
one route is proven, keep server-only uncertainty distinct from a verified empty
inventory and hold new recovery writes for that destination.

## Live storage/API discrepancy

On 2026-10-29, an authenticated check of an existing private repository found
21 checkpoint refs on Entire's Git service. All 21 were fetched into an isolated
private repository. The installed CLI's API-backed checkpoint explanation read
15 successfully and reported six unavailable. Direct requests to the same
checkpoint-detail route returned HTTP 404 for those six and HTTP 200 for a
successful control. This was not a general authentication or transport failure.

Both groups contained October 26 and October 29 records. Root/session metadata
was present in Git for all 21, and inspected metadata variants overlapped across
the successful and missing groups. Neither age nor those schema variants
explained the discrepancy. The cause remains unresolved; Git presence alone
must not count as successful server ingestion. Raw responses, IDs and repository
inventory remain in private evidence. This observation also does not establish
that all service-visible checkpoints are represented by those Git refs.
