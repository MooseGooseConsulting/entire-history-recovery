# Entire History Recovery

Research and tools for recovering agent session history into Entire, associating sessions with commits where evidence supports it, and verifying reports against Git history.

Start with the [project goals](GOALS.md) and the authoritative [implementation and rollout plan](docs/plans/history-recovery.md), then consult the [field and ingestion catalog](docs/entire-field-catalog.md) for checkpoint fields, sources, and integration boundaries. The plan covers missing original checkouts, existing Entire records, deduplication, review gates and the conditions for bulk ingestion.

For implementation evidence, see the [one-session prototype](docs/one-session-prototype.md), [existing-coverage contract](docs/existing-coverage.md), and [server inventory boundary](docs/server-inventory-boundary.md). These describe verified behavior and limits; the rollout plan tracks remaining work.

This public repository is for source code, synthetic fixtures, and sanitized documentation. Keep private transcripts, credentials, inventories, and recovery manifests outside it.
