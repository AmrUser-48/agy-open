---
name: tui-ux
description: Design lightweight terminal user interfaces with clear navigation, discoverable keyboard controls, and accessible interactive workflows.
user-invocable: true
---
When designing terminal UI:
- Make primary actions discoverable without memorizing slash commands.
- Keep keyboard navigation consistent: arrows or j/k to move, Enter to act, Esc to go back.
- Show contextual key hints and visible focus/selection.
- Keep the common path short; use sensible defaults and allow advanced details on demand.
- Avoid blocking I/O in the event loop and preserve line-mode terminal compatibility.
- Make every action available through the TUI; slash commands should be shortcuts, not the only entry point.
