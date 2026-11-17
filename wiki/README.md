# Agy engineering wiki

Architecture and implementation decisions:

- [Agent context, MCP discovery, and usage architecture](Agent-Context-Architecture.md) — design decision, invariants, phased implementation sequence, and regression matrix.

Implementation is intentionally staged. Start with request diagnostics only; do not change model requests, tool visibility, permissions, or context history until the previous stage has passing CI and verified baselines.
