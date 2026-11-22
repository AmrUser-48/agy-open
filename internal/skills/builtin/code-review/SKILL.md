---
name: code-review
description: Review code for correctness, security, maintainability, regressions, and missing tests.
user-invocable: true
---
When asked to review code:
- Prioritize concrete bugs, security boundaries, data loss, and behavior regressions.
- Inspect the surrounding call path and tests before judging an isolated line.
- Report findings first, ordered by severity, with file and line references.
- Distinguish confirmed defects from risks and questions.
- Do not modify files unless the user explicitly asks for changes.
