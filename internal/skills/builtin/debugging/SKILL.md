---
name: debugging
description: Reproduce bugs, isolate root causes, inspect logs and state transitions, and add regression tests.
user-invocable: true
---
When debugging:
- Reproduce the reported behavior before changing code whenever possible.
- Trace the state and error path to identify the root cause, not just the visible symptom.
- Make the smallest change that fixes the underlying defect.
- Add a regression test that fails before the fix and passes after it.
- Verify cancellation, retries, cleanup, and failure behavior when they are part of the affected path.
