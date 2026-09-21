# CWapi Agent Core Protocol

CWapi Agent mode connects Web GPT to OpenAI-compatible requests issued by local software. Web GPT is the reasoning controller; CWapi owns request transport, lifecycle bookkeeping and protocol conversion.

## Bridge and requests

- `agent_open()` opens or resumes the current logical bridge. The internal bridge generation is CWapi-owned.
- `agent_exchange(...)` submits results and receives request work. Every returned request has a stable `request_id` and a `delivery` counter.
- Requests may include bounded inline raster images. Request JSON uses ordered `image_ref` content parts and the same tool result carries matching native MCP `ImageContent`; original bytes and MIME are preserved without recompression, resizing, transcoding or OCR. Generic file attachments remain unsupported.
- Process every returned request independently. A response must use the matching `request_id`; never mix tool calls, results or completion state between requests in the same batch.
- `delivery > 1` is redelivery of the same request, never a new task.
- A resumed/redelivered request may include `previous_state`, `resume_reason` and `last_activity`; continue from the preserved request context rather than restarting work.
- Bridge lifetime and request lifetime are separate. Heartbeat renews bridge liveness only. Request activity is renewed by delivery, progress, stream chunks or a response, and every request also has a non-extendable hard lifetime. A temporary bridge loss must not erase an active request.

## Event semantics

CWapi keeps protocol events distinct:

- `heartbeat`: bridge/runtime liveness only; it never extends a request activity deadline or hard deadline and does not require a natural-language assistant message.
- `progress`: request-scoped progress for human observability. It refreshes request activity, is tracked separately per request, and is neither heartbeat nor completion.
- `stream`: for an OpenAI request with `stream=true`, `agent_exchange.stream_chunks` may submit incremental content/tool-call deltas before the final response; CWapi forwards them immediately to the local SSE client.
- `tool_call`: a model-requested local tool invocation.
- `tool_result`: the local tool's result or structured tool error.
- `completion`: structured terminal success for the current request.
- `error`: protocol/request error information. Retryable errors do not silently terminate an otherwise recoverable request.

## Tool calls and completion

- Tool calls are executed by the local OpenAI-compatible client, not by this MCP server.
- Preserve tool-call IDs and tool-result associations exactly.
- `function.arguments` may arrive as an OpenAI JSON string or an internal JSON object. CWapi canonicalizes it once into an object and later emits the OpenAI string form once.
- Streaming tool-call argument fragments must be concatenated completely before JSON parsing. When `stream=true`, send incremental text/tool fragments through `stream_chunks` as they become available, then still submit one final structured response for terminal validation and finish reason.
- A tool-call parse/mapping failure is returned as a structured error so Web GPT can correct the call. It must not silently strand the request.
- A request is finished only by a structured `completion` state/event, not by matching words such as “completed” in assistant text.
- `agent_close()` closes the current bridge handle; active request state is preserved for resume until it reaches a terminal state or its request lifetime expires.

## Skills

- `load_skill(name)` loads one startup-cached global Skill by its Skill ID.
- Load only Skills listed in the startup Skill inventory. A missing Skill returns `SKILL_NOT_FOUND`.
- Loading a Skill changes neither request state nor local project state.
