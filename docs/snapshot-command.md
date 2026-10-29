# Local sealed-snapshot command

Build `cmd/recover-one` with Go 1.27.3. This is an experimental **local-only**
command for the [rollout plan's first slice](plans/history-recovery.md). It does
not resolve historical repository ownership, reconcile all existing server
history, or publish checkpoints. Do not use it as the bulk ingestion runner.

```text
recover-one snapshot --source PATH --inactive [--dir PRIVATE_DIR]
recover-one import --snapshot SEALED_PATH --repo CHECKOUT --anchor FULL_SHA
recover-one import --snapshot SEALED_PATH --repo CHECKOUT --anchor FULL_SHA --write
```

Select a genuinely inactive source first. `--inactive` is the caller's assertion,
not a way to stop a running agent. Sources still being written need a consistent
native export. Snapshot intake validates a single native Codex session, compares
source identity/stat/digest around copying, and publishes a content-addressed
read-only copy without overwriting another snapshot. The original stays unchanged.
Multiple session headers are held until that source format is understood.

The default destination is `entire-history-recovery` under the user config
directory. A new private directory is restricted to its owner on Unix and to the
current user, SYSTEM and Administrators on Windows. Existing directories are
verified without changing permissions; unsafe directories are rejected. Snapshot
publication uses a hard link in that directory, so unsupported filesystems fail
rather than falling back to an overwrite. Identical snapshots are verified and
reused; symlink snapshot files and corrupt content-addressed files are rejected.

Import previews by default. `--write` writes checkpoints only into the explicit
checkout; the caller must verify the source belongs there. The explicit anchor
is a display commit, not evidence that the session authored it. Snapshot digest
verification is carried through the importer's reads, including its later reread.
Preview, write and retry use separate command invocations on Windows so the
process boundary releases repository handles. In-process Windows suite cleanup
remains an open limitation.

Review order: `internal/snapshot/seal.go` and platform directory checks, then
`ImportCodexVerified` in `internal/recovery/one.go`, then the command. The helper
is independently tested for tampering, source changes, symlinks and private
directory handling. Actual-session local coverage is recorded in the
[prototype notes](one-session-prototype.md); server coverage remains a separate
[unproven boundary](server-inventory-boundary.md).

A standalone Windows command run over 12 actual inactive, single-header native
sessions completed snapshot -> preview -> local write -> real write retry:
37 previewed, 37 written, zero new on retry and 37 skipped. Original source hashes
and checkpoint ref hashes remained unchanged, staging-file count was zero, and
the temporary destination was removed after command exit. This run did not
upload any session to GitHub or Entire's server.
