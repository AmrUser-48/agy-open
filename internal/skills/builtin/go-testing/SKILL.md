---
name: go-testing
description: Design, write, run, and diagnose Go tests using idiomatic table-driven tests and focused package verification.
user-invocable: true
---
When working on Go tests:
- Prefer small table-driven tests for behavior with multiple cases.
- Test observable behavior, edge cases, cancellation, and error paths.
- Keep tests deterministic; avoid sleeps when channels or controlled clocks can be used.
- Run the focused package tests first, then go test ./... when practical.
- Never claim a test passed unless it was actually run; state clearly when verification is unavailable.
