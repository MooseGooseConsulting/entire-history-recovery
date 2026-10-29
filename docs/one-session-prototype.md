# One-session import prototype

The `internal/recovery` package tests a standalone wrapper around Entire CLI v0.12.2's Codex importer. It supplies one previously selected native session directly to Entire's existing parser and checkpoint writer, bypassing the current-path and age filters. It reads the session ID from the native header and preserves the original source file.

The wrapper adds a model only for a single-turn session whose native `turn_context` records one unambiguous model. Context events can appear before the prompt, so multi-turn model correlation is deferred until native turn evidence is proven. Entire's stock Codex turn parser does not set the model. Missing, conflicting, or uncorrelated values stay empty.

Run the synthetic fixture with `go test ./...`. It tests an old session whose original directory is gone, local checkpoint metadata and transcript readback, sanitized encrypted payloads, token usage, and duplicate-free rerun. The optional private-session test is skipped unless a local source path is explicitly supplied through `RECOVERY_TEST_CODEX_SOURCE`; never commit a real session or its path.

This is a local proof, not a production import tool. It does not map a session to a real destination, verify remote checkpoint IDs, push anything, validate server readback, or attribute work to commits. Its caller is responsible for a verified repository mapping. No command-line interface is exposed yet. Entire's redaction configuration is process-global, so safe per-repository configuration must be established before multi-repo use. A real destination and its data exposure must be verified before writing or syncing.
