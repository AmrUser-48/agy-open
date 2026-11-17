# Agent Context, MCP Discovery, and Usage Architecture

Status: accepted design direction; implementation is staged.
Target branch: `work`.
Baseline: `d93df49ca38bc2c2bb93cd901398eb04a09a4f76`.
Last verified baseline CI: run #2069, successful.
No changes to `main`, no release, and no tag are part of this work.

## 1. Goal

Fix avoidable model-request growth and improve long-running agent reliability without reducing tool capability.

The design must preserve:

- `maxTurns=500` capability. Turns are not a token or context budget.
- Every configured MCP server remains connected and every discovered MCP tool remains callable through the complete registry.
- Existing role, approval, permission, workspace, and mutation controls remain authoritative.
- Complete execution traces and recoverable original tool outputs.
- Existing Gemini OAuth, Gemini API, AI Studio/Interactions, OpenAI OAuth, Groq, NVIDIA, and OpenAI-compatible behavior.
- Existing session files and resume compatibility.

This is an incremental redesign of Agy's existing agent loop. It is not a framework replacement.

## 2. Evidence from the current implementation

The following was verified in the source at the checkpoint:

1. `internal/agent/agent.go`: `RunContext` refreshes configured MCP servers, appends the user message to `a.messages`, and constructs a new model request on every generation cycle using the complete in-memory history.
2. `declarationsAsTools()` appends every discovered MCP schema to the declarations sent to the model when the active role and global permissions allow MCP.
3. `internal/mcp/client.go`: `mcp.Manager` independently owns the connected clients, complete tool map, server states, and the `Call` path. It can remain the authority for tool availability while model-facing declarations become selective.
4. `internal/openaicompat/client.go`: provider request construction serializes request history and flattens declared functions into OpenAI-compatible tool definitions. Current response decoding does not retain the provider usage object.
5. `internal/gemini/client.go` defines `Response.Usage`, but the current public Generate/stream path returns content only. `internal/gemini/stream.go` does not retain streamed `usageMetadata`.
6. The agent's `modelClient` interface currently returns `gemini.Content` and an error; it has no provider-neutral usage result.
7. `internal/session/session.go` persists a structured transcript and tool activity. gzip/Zstandard are storage compression only. They do not reduce the model request.
8. `Agent.ContextChars()` and `/context` provide a character estimate, not tokenizer-backed context usage.
9. `internal/openaicompat/nvidia_rate_limit.go` estimates token use from serialized bytes divided by four plus reserved output. This is a heuristic, not provider metering.
10. Groq currently has one configured TPM budget, not independently configurable ITPM and OTPM budgets.

The 105 tools observed in the user's test (91 GitHub and 14 Git) demonstrate why model-visible definitions must be decoupled from the actual registry. Their schemas have not yet been measured with a provider tokenizer; do not attribute a precise portion of the reported 24,890/36,430 input tokens to schemas without measurement.

## 3. Architecture decision

Use a hybrid of established public patterns:

| Reference | Adopt | Do not copy |
|---|---|---|
| Anthropic Claude tool search | Deferred discovery and on-demand full tool definitions | Anthropic-only API dependencies |
| OpenAI Codex | Context accounting, token thresholds, usage breakdown, compaction discipline | Entire Responses API runtime |
| Gemini CLI | MCP tool filtering concepts, selective tool-result handling, context telemetry | Static allowlists as a substitute for discovery |
| OpenCode | Summary plus a preserved recent tail; stale tool-output pruning | Its full session/runtime architecture |
| Aichat | Simple interactive function calling and compression behavior as a benchmark | Its simpler one-shell-tool assumptions |

Agy should retain its current agent loop, provider identities, MCP client, permission manager, event/trace system, and session store.

### 3.1 Deferred MCP discovery — preferred approach

Expose a compact discovery function initially, such as `mcp_search_tools`.

- Search the manager's complete registry using deterministic name, server, and description matching.
- Return a compact ranked list containing stable qualified names, server, short description, and schema-size metadata.
- When a tool is selected, make its actual function declaration and full schema available to the model.
- Keep its qualified name and schema available for subsequent turns while needed.
- Execute the original namespaced function through the existing `mcp.Manager.Call` route.
- Apply the existing `toolAllowed`, approval, role, read-only, and worker restrictions to both discovery and execution.
- If the model needs another tool, it can search again. There is no fixed small cap on the manager registry or total accessible tools.

Do not replace typed model-visible functions with one generic `mcp_call(server, name, arguments)` as the default architecture. Generic dispatch saves schema tokens but weakens provider-side argument validation and makes tool calls less transparent. It may remain an optional fallback for providers with strict tool-definition constraints.

Discovery must never confer permission. A result being discoverable does not mean it is executable.

### 3.2 Request-scoped tool activation

The MCP manager remains the source of truth. A separate request-scoped activation view determines which full schemas are currently exposed to the model.

The activation view must be rebuilt or validated after context compaction and session resume. It must not own or terminate MCP processes. A stale activated name must be checked against the live registry at execution time.

Provider differences matter: some APIs treat tool definitions as stateless request input; some support provider-managed deferred loading. Agy's own registry and discovery path must be the portable baseline.

## 4. Context management

### 4.1 Separate canonical history from active model context

- **Canonical history:** the complete existing transcript, structured tool calls/results, approvals, worker events, provider-native steps, and recoverable outputs.
- **Active model context:** a request-time view containing essential instructions, a summary of older work, recent exact conversation turns, valid function-call/result pairs, relevant tool definitions, and a bounded selection of tool outputs.

Do not delete or rewrite canonical session records to compact the model request.

### 4.2 Compaction policy

Use a Codex/OpenCode-style token-aware policy:

1. Determine the selected model's known context window where reliable metadata exists.
2. Estimate active input size and reserve output capacity plus a configurable safety margin.
3. Trigger compaction before the request approaches the effective input budget.
4. Summarize older complete interaction groups, retaining decisions, constraints, task state, important paths, command results, errors, and unfinished work.
5. Preserve a recent exact tail of user/assistant turns and tool interactions.
6. Replace old oversized tool output in active context with an explicit reference to the canonical output record or a durable output artifact.
7. Verify the compacted provider request is structurally valid before sending it.

If model context size is unknown, use a conservative configurable threshold and clearly label it as estimated. Never assume a context window based only on a model-name substring.

### 4.3 Structural invariants

- Never split a function call from its matching function response.
- Preserve ordering, call IDs, and tool names.
- Preserve model-specific thought signatures or other fields needed for valid replay.
- Preserve AI Studio Interactions native steps; do not reconstruct them as ordinary text.
- Do not compress away recent exact tool evidence needed for the next action.
- Do not compact during a tool call or between a call and its result.
- Keep original tool output recoverable and retain the execution trace even when an abbreviated result is sent to the model.

Disk gzip/Zstandard compression is independent from this context policy.

## 5. Usage accounting and request telemetry

Introduce a provider-neutral per-generation usage record with fields for:

- Input/prompt tokens.
- Cached input tokens.
- Cache-write input tokens, if reported.
- Output/completion tokens.
- Reasoning tokens, if separately reported.
- Total tokens.
- Provider-specific counters and metadata.
- Whether the usage is provider-reported or estimated.

Aggregate usage per generation, run, and session. A missing provider metric must remain unknown, not silently become zero.

### 5.1 Request-size diagnostics

Before behavior-changing optimization, measure:

- System-instruction bytes.
- Built-in tool-schema bytes.
- MCP schema bytes, split into activated and available-but-not-advertised definitions.
- Conversation-history bytes.
- Tool-call argument bytes.
- Tool-result bytes.
- Current user-prompt bytes.
- Total serialized provider request bytes.

Use the exact serialized wire payload size where the transport layer can observe it. If a metric is computed before provider-specific conversion, label it as an Agy request-model estimate, not exact wire bytes.

Log provider, model, turn number, request ID where available, counts, and measurement method. Never log API keys, OAuth tokens, raw request bodies, full prompts, arguments, or tool output in telemetry.

Add tests that prove the counters match the actual serialized payload and that telemetry contains no sensitive content.

## 6. Groq quota accounting

Treat quota limits as organization/model/account-specific; do not hard-code public-tier assumptions.

Represent independently where applicable:

- Requests per minute (RPM).
- Requests per day (RPD).
- Input tokens per minute (ITPM).
- Output tokens per minute (OTPM).
- Combined tokens per minute (TPM).

Use provider-reported usage after completion to reconcile local estimates. Preflight estimates must distinguish input estimate from reserved output. A 413/rate-limit error should report safe request-size diagnostics and the applicable quota signal, without dumping credentials or raw prompt content.

Check current Groq documentation and real response behavior before enabling streaming usage options. Do not assume every OpenAI-compatible streaming provider implements the same `stream_options.include_usage` behavior. A non-streaming usage path and mock tests are required even if streaming usage is unavailable.

## 7. TUI and observability

- `/context` should report estimated active context, model limit (only if known), safety reserve, remaining estimated capacity, history summary status, and active MCP schema count.
- `/usage` should report last generation and accumulated session provider usage, clearly separating actual from estimated values.
- Request diagnostics should be concise, useful for debugging 413 errors, and available without exposing prompts or secrets.
- Existing tool trajectory must continue showing the exact command/function and actual output, not just a generic “tool executed” event.

Do not mislabel account quota endpoints as per-request token usage.

## 8. Phased implementation plan

### Stage 0 — instrumentation only

No request behavior changes.

- Add safe request-size breakdown metrics.
- Measure actual serialized wire payload bytes in provider transports where possible.
- Add a typed diagnostics record and tests.
- Preserve existing provider calls and tool declarations exactly.
- Do not add automatic context truncation or compaction.

Exit gate: baseline sizes can be inspected for no MCP, built-in tools, Git MCP, GitHub MCP, and both MCP servers. Metrics are proven to describe the measured representation and do not expose content or credentials.

### Stage 1 — MCP discovery and schema activation

- Add compact search over the complete MCP registry.
- Add request-scoped schema activation.
- Keep all actual MCP execution and authorization paths unchanged.
- Add deterministic ranking and ambiguity behavior.
- Verify all tools remain accessible, including tools beyond any single response page.

Exit gate: tests demonstrate complete registry preservation, discovery of every test tool, activated schema integrity, execution of selected tools, and unchanged permissions.

### Stage 2 — provider-neutral usage accounting

- Add usage to provider response types and preserve it through streaming/non-streaming decoders.
- Extend the provider interface compatibly, avoiding unnecessary changes to tool execution interfaces.
- Aggregate per turn/run/session.
- Clearly represent unknown and estimated counters.

Exit gate: mock fixtures for Groq, NVIDIA-compatible, OpenAI, Gemini Developer API, and Gemini OAuth paths verify parsing and aggregation. Providers that do not return usage must remain explicitly unknown.

### Stage 3 — context manager and compaction

- Introduce a dedicated component to build an active request view.
- Add model context metadata and output reserve handling.
- Add safe compaction boundaries and summary records.
- Retain complete canonical history and output artifacts.
- Preserve provider-specific replay requirements.

Exit gate: long-session tests demonstrate request-size reduction, valid call/result structure, successful resume, and unchanged canonical history.

### Stage 4 — Groq quota handling

- Split applicable limits into separate settings.
- Keep the current shared limiter behavior where valid, but make reservations dimensionally correct.
- Reconcile actual usage and response quota headers.
- Add useful 413 diagnostics and retry behavior.

Exit gate: mocked tests cover independent ITPM/OTPM, combined TPM, RPM/RPD, reset headers, concurrent workers, requests larger than a single-window budget, and unknown limits.

### Stage 5 — TUI and end-to-end regression

- Improve `/context`, `/usage`, and configuration menus.
- Add user-visible explanations for actual versus estimated values.
- Verify ordinary sessions and long-running agent runs.

## 9. Regression matrix

| Scenario | Required assertion |
|---|---|
| Fresh Groq Qwen, no MCP | Valid simple response; baseline request measured |
| Fresh Groq GPT-OSS, no MCP | Valid simple response; baseline request measured |
| Built-in tools only | Existing tool behavior and approvals unchanged |
| Git MCP | All 14 observed tools stay registered and callable |
| GitHub MCP | All 91 observed tools stay registered and callable |
| Both MCP servers | All 105 observed tools stay registered and callable |
| Tool search | Finds tools by exact name, partial name, server, and description |
| Repeated turns | Only relevant schemas are exposed; activated schema can be rediscovered |
| Tool execution | Same namespaced route, role gates, approvals, and execution trace |
| Large tool output | Active request can reference abbreviated content; full output remains recoverable |
| Compaction boundary | Call/result pairing, ordering, IDs, and provider-native state remain valid |
| Resume after compaction | Session reload rebuilds active context and complete MCP registry |
| 500-turn capability | No reduction of maxTurns; long task continues subject to separate runtime/tool limits |
| Gemini OAuth | Existing tool loop and schema sanitization continue to work |
| AI Studio Interactions | Native steps remain preserved and replayable |
| NVIDIA | Existing quota and request behavior do not regress |
| Session formats | Existing gzip, zstd, and uncompressed sessions remain readable |
| Permissions | Discovery never bypasses role/approval/read-only/workspace controls |
| Observability | No secrets, raw prompts, arguments, or tool outputs in metrics |
| CI | Formatting, all tests, vet, static Linux AMD64 build pass |

## 10. Explicit non-goals and prohibitions

- Do not lower the 500-turn capability to solve context growth.
- Do not disable MCP servers or permanently hide tools to fit a small context.
- Do not impose a fixed cap on the MCP registry or total accessible tools.
- Do not replace MCP with a generic dispatch function as the only execution interface.
- Do not delete canonical history to compact active context.
- Do not confuse gzip/Zstandard storage compression with context compaction.
- Do not claim exact token counts from bytes divided by four.
- Do not claim provider usage if the provider did not return it.
- Do not put sensitive content into diagnostic logs.
- Do not bundle all phases into one large patch.
- Do not modify `main`, merge, tag, or publish a release as part of these stages.

## 11. Upstream references

Use these as implementation references, not as evidence of private internals:

- Aichat configuration: https://github.com/sigoden/aichat/blob/main/config.example.yaml
- Aichat FAQ: https://github.com/sigoden/aichat/wiki/FAQ
- Aichat configuration guide: https://github.com/sigoden/aichat/wiki/Configuration-Guide
- Gemini CLI MCP servers: https://github.com/google-gemini/gemini-cli/blob/main/docs/tools/mcp-server.md
- Gemini CLI context compression: https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/context/chatCompressionService.ts
- Gemini CLI context telemetry: https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/core/loggingContentGenerator.ts
- Codex context window: https://github.com/openai/codex/blob/main/codex-rs/core/src/session/context_window.rs
- Codex token usage UI: https://github.com/openai/codex/blob/main/codex-rs/tui/src/token_usage.rs
- Codex agent-loop compaction: https://openai.com/index/unrolling-the-codex-agent-loop/
- Anthropic tool context management: https://platform.claude.com/docs/en/agents-and-tools/tool-use/manage-tool-context
- Anthropic advanced tool use: https://www.anthropic.com/research/advanced-tool-use
- OpenCode compaction: https://opencode.ai/docs/compaction/
- OpenCode configuration: https://opencode.ai/docs/config/
- Groq API reference: https://console.groq.com/docs/api-reference#chat-create
- Groq rate limits: https://console.groq.com/docs/rate-limits

## 12. Implementation log

- Baseline checkpoint `d93df49`: architecture research recorded in `ai.log`; no source changes.
- CI run #2069 for the baseline commit: completed successfully.
- Documentation is kept under `wiki/` in the repository because the connected GitHub integration does not provide a wiki-page write endpoint. This is versioned repo documentation; it is not a claim that the separate GitHub Wiki site was edited.
- Stage 0 is the first implementation step. No behavior-changing stage should begin until Stage 0 has a clean CI result and measured baseline.

## Stage 0 implementation status — 2026-11-17

Status: **completed; CI verified**.

- Provider-neutral request breakdown and `/context` display: commit [54308bf](https://github.com/AmrUser-48/agy-open/commit/54308bf4a65a722b787740398d7a57905f6e0602), CI run [#2073](https://github.com/AmrUser-48/agy-open/actions/runs/43985245052) — success.
- Exact OpenAI-compatible Chat Completions JSON-body length, carried in a non-serialized diagnostics pointer and shown separately in `/context`: commit [c56fcfc](https://github.com/AmrUser-48/agy-open/commit/c56fcfca69a8fd932b0569e16b69c3bda9348879), CI run [#2075](https://github.com/AmrUser-48/agy-open/actions/runs/43985745253) — success.
- The exact body measurement covers Groq, NVIDIA, OpenRouter, and other clients using the OpenAI-compatible Chat Completions adapter. It measures the byte slice passed as the HTTP request body, excluding headers; it is not a token count.
- Gemini Developer API, Antigravity OAuth, AI Studio/Interactions, OpenAI Responses/OAuth, and Gemini Live are not yet instrumented at their transport boundaries. For those adapters the displayed Agy request JSON size is only the provider-neutral request representation.
- Tests verify the OpenAI-compatible body byte count against the received HTTP body and ensure the internal diagnostics pointer is not serialized into the request.
- Stage 0 did not change request contents, tool visibility, MCP connectivity, permission checks, rate-limiter reservations, context history, or turn limits. Live account-level baselines still need to be captured on the user's configured providers and MCP servers.

Next stage: deterministic compact MCP discovery with dynamic activation of real tool schemas. Retain the full MCP registry and existing execution/permission route. Add focused tests before enabling it by default.


## Stage 1 implementation status — 2026-11-17

Status: **implemented; CI verified**.

- Commit [783b606](https://github.com/AmrUser-48/agy-open/commit/783b606faaf3eedd52131fc6f266c3b6ecac9cd3), CI run [#2077](https://github.com/AmrUser-48/agy-open/actions/runs/43991533108) — success.
- Added `mcp.Manager.SearchTools`: case-insensitive deterministic ranking across qualified/remote tool names, server names, and descriptions. Search pages are limited to 25 entries for bounded responses, with offset pagination and no cap on the complete registry or total reachable tools.
- Added the `mcp_search_tools` model function for stateless providers. A search activates the real namespaced function declarations and their complete input schemas for the next generation request. Subsequent searches add further schemas for the rest of that run.
- Unactivated remote function calls are rejected. Selected tools still execute through the existing `mcp.Manager.Call` path and must pass `toolAllowed`, role restrictions, workspace/worker restrictions, and the existing MCP approval check. Discovery itself does not grant execution permission.
- Global disabled/read-only modes, read-only roles, forced-read-only workers, and isolated non-writer workers do not receive MCP discovery.
- Gemini Live retains the full legacy MCP declaration set. Its function declarations are sent once during WebSocket setup; the current transport does not support adding schema declarations mid-session. Other stateless provider adapters use lazy activation.
- MCP `tools/list` now follows pagination until the server stops returning a cursor, detects repeated cursors, and no longer silently discards tools solely because one input schema exceeds 64 KiB.
- Regression coverage verifies exact/partial/server/description matching, deterministic paging across a synthetic 131-tool registry, loading a server registry spread over 101 MCP pages, role/permission gates, initial schema deferral, next-request activation, and execution of the selected tool.
- No changes to `maxTurns`, provider identities, the canonical session history, approval semantics, or `main`. Live validation against the user's configured Git and GitHub MCP servers remains outstanding.

Stage 1 exit gate is met for the unit/integration fixtures and CI. The real 14-tool Git + 91-tool GitHub configuration should still be exercised in an interactive run before treating live-server behavior as verified.
