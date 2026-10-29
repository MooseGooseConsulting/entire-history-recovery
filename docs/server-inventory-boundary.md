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
