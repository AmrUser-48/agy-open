# Agy engineering wiki

Canonical project wiki: https://github.com/AmrUser-48/agy-open/wiki

This `wiki/` directory is the version-controlled documentation mirror in the source repository. It is not the separate GitHub Wiki site; keep mirrored pages aligned when the canonical wiki is updated.

Architecture and implementation decisions:

- [Agent context, MCP discovery, and usage architecture](Agent-Context-Architecture.md) — design decision, invariants, phased implementation sequence, and regression matrix.

Implementation is intentionally staged. Each stage must pass CI before the next begins. Preserve the complete MCP registry, canonical session history, existing authorization path, and the 500-turn capability.
