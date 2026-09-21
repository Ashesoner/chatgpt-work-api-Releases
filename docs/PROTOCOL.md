# CWapi 2.0 Protocol

2.0 使用 MCP Streamable HTTP 与一个 localhost OpenAI-compatible Provider。所有 listener 只绑定 `127.0.0.1`。

## MCP routes

```text
http://127.0.0.1:<mcp-port>/mcp/coding/<coding-token>
http://127.0.0.1:<mcp-port>/mcp/agent/<agent-token>
```

- route 必须 exact match；错误、缺失或交叉 token 返回 not found；
- 每条 route 使用独立 stateless `mcp.Server`；
- Coding route 只公布 Coding tools；Agent route 只公布 Agent tools；
- 未启用 Agent surface 时 Agent route 不暴露；
- Coding 与 Agent 只接受本文件定义的当前 2.0 MCP/API surface。

## OpenAI Secure MCP Tunnel

- `tunnel` 配置与 Coding route 绑定；`agent_tunnel` 配置与 Agent route 绑定；
- 两个 Tunnel 各自使用独立 Tunnel ID、Runtime API key、profile 和 `main` channel；
- profile 中的 `main` 只指向对应的 localhost route，不会把 Coding 与 Agent tool catalog 合并；
- ChatGPT Web 应选择 Tunnel 连接方式，不能直接填写 `127.0.0.1` 的 Server URL；
- 两组 Runtime API key 只进入各自 tunnel-client 子进程环境，不进入 config 或 profile 明文。

## Coding tools

Coding MCP 正式 tool catalog 固定为：

```text
coding_open
coding_exec
coding_status
coding_close
load_skill
```

### `coding_open`

```json
{
  "repository_url": "https://github.com/owner/repo",
  "target_ref": "refs/heads/feature",
  "expected_commit": "optional-full-40hex",
  "resume": false
}
```

返回 `repository,target_ref,resolved_commit,current_head,tracked_dirty,resumed,state`。MCP 公共协议不暴露 `coding_id`。CWapi 内部仍为每个 branch-aware active Coding session 生成唯一 internal session ID，并以 `canonical repository + canonical target_ref` 作为 active workspace identity，用于生命周期、并发、取消与 stale-operation 防护。

同一 repository 的不同 branch 可以同时 active；同一 repository + 同一 canonical target ref 仍最多一个 active Coding session。ChatGPT conversation 结束不是 CWapi session 终止信号，因此旧 conversation 消失后该 branch 的 active owner 可能继续存在。

当相同 repository + target ref 已有 active session：

- `resume=false` 返回 `CODING_WORKSPACE_BUSY`；
- 兼容的 `resume=true` 复用原 active internal session，不再次 prepare workspace，并返回 `resumed=true`；
- active command 正在运行时可返回 `state=busy`；
- target ref 不兼容返回 `CODING_RESUME_TARGET_MISMATCH`；
- supplied `expected_commit` 与 active session baseline 不一致返回 `CODING_RESUME_COMMIT_MISMATCH`；
- workspace 正在 opening 时返回 opening/busy 对应错误；
- session 正在 closing 时返回 `CODING_SESSION_CLOSING`。

`main` 与 `refs/heads/main` 视为同一 heads target。新 Web GPT conversation 使用同一 `repository_url + target_ref` 调用兼容的 `coding_open(..., resume=true)` 即可恢复该 branch，无需知道旧随机 ID。

### `coding_exec`

```json
{
  "repository_url":"https://github.com/owner/repo",
  "target_ref":"feature",
  "action":"run",
  "command":"go",
  "argv":["test","./..."],
  "cwd":"optional/relative/path",
  "timeout_seconds":120
}
```

`target_ref` 可选。指定时按与 `coding_open`/WorkspaceIdentity 相同的 canonical 规则精确路由；目标 branch 不 active 时返回 `CODING_SESSION_NOT_ACTIVE`，不会 fallback 到其它 branch。省略时，仅当该 repository 恰好只有一个 active branch 才保持旧的 repository-only 兼容行为；多个 branch 同时 active 时返回 `CODING_SESSION_AMBIGUOUS`。

省略 `action` 等价于兼容的 foreground `run`，返回 `state,exit_code,stdout,stderr` 以及截断信息。预计超过普通 MCP 调用时长的任务应使用 `action=start`，立即获得 `state=running,process_id,pid,started_at`。`action=status` 可附上上次返回的 `stdout_cursor/stderr_cursor`，只返回 cursor 之后的新 stdout/stderr，并给出新的绝对 cursor 与每个输出流各自的截断标记。`action=stop` 使用同一 repository/target selector 与 `process_id`。最多 16 个 persistent process；workspace close 与应用退出会停止对应进程树。同一个 workspace session 同时只允许一个 foreground operation；不同 branch 的独立 session 可并行。`command` 与 `cwd` 的远端 path syntax 只接受 `/`；argv 逐项传递，不做 shell 拼接。foreground 命令默认超时 120 秒，显式 `timeout_seconds` 范围为 1–600 秒；persistent start 不接受 timeout。

调用固定进入 bundled private Codex app-server 的 model-free `command/exec`。它不创建 thread/turn，不调用 Codex agent、auth、account 或 model API。CWapi 在启动前检查解析后的最终 executable/argv/CWD。SAFE 使用 `workspaceWrite`、合成 profile、隔离配置与 workspace-lifetime cache；FULL 使用 `dangerFullAccess` 和剥离内部 secret 后的当前 Windows 用户开发环境。网络能力独立、默认关闭。Remote Git Rewrite 也是独立、默认关闭的高级能力，只放开 direct force/delete remote updates；危险 transport、receive-pack 注入、CWapi safety refs 与内部路径始终拒绝。

源码、Markdown、JSON、日志、配置等 workspace 内容通过 `coding_exec` 的 exact 读取命令交付给 Web GPT，而不是实例化成 ChatGPT 文件资源。

### `coding_status`

```json
{"repository_url":"https://github.com/owner/repo","target_ref":"feature"}
```

`target_ref` 可选，路由规则与 `coding_exec` 相同：精确 target 不存在时返回 `CODING_SESSION_NOT_ACTIVE`；省略时单 active branch 兼容，多 active branch 返回 `CODING_SESSION_AMBIGUOUS`。

返回 `state,repository,target_ref,resolved_commit,current_head,current_branch,detached,tracking_head,tracked_dirty,divergence,last_error,persistent_processes`。idle 状态会重新读取所选 branch workspace 的本地 Git truth 并回写 active session cache，因此后续 `coding_open(..., resume=true)` 不会回退到旧 HEAD/branch/dirty。`persistent_processes` 只列该 workspace 的 active persistent process 的 `process_id/state/command/pid/started_at/elapsed_seconds`，不回显 argv。当 `state=busy` 时额外返回 `active_action,active_command,active_started_at,active_elapsed_seconds`。该操作不 fetch，也不返回 Codex transcript。目标 branch 没有 active session 时返回明确的 not-active 错误。

### `coding_attachment`

```json
{"repository_url":"https://github.com/owner/repo","target_ref":"feature","paths":["screenshots/ui.png"]}
```

`target_ref` 可选，路由规则与 `coding_exec`/`coding_status` 相同。该工具只读取所选 active workspace 内的受限 raster image。成功时返回 `repository,target_ref`、image metadata 与 `total_bytes`，并在 MCP tool result 中追加原生 `ImageContent`。PNG/JPEG/GIF/WebP 等支持格式保持原始图片字节与 MIME；CWapi 不压缩、不缩放、不转码、不 OCR。任一 path 不是图片时返回 `CODING_ATTACHMENT_IMAGE_ONLY`；普通源码、Markdown、JSON、日志、PDF、ZIP、Office 等文件不生成 `EmbeddedResource`。

### `coding_close`

```json
{"repository_url":"https://github.com/owner/repo","target_ref":"feature"}
```

`target_ref` 可选，使用与 exec/status 相同的 branch-aware 路由；不会 fallback 到其它 branch。关闭精确选中的 active internal session owner；active operation 会先被取消并等待收口。该操作不 reset/clean workspace，不删除 durable workspace，也不修改或删除用户未提交内容。省略 `target_ref` 且 repository 没有任何 active branch 时返回幂等友好的 `state=no_active_session`；显式指定一个未 active 的 `target_ref` 时返回 `CODING_SESSION_NOT_ACTIVE`，不 fallback。

### `load_skill`

Coding 与 Agent 共用该工具。输入 Skill ID，返回 CWapi 启动时缓存的全局 Skill 内容；具体 Skill 不会默认塞进 MCP initialization context。修改 `prompts/` 后需要重启 CWapi。

## Agent tools

Agent MCP 对外暴露 4 个工具：`agent_open`、`agent_exchange`、`agent_close`、`load_skill`。`load_skill` 与 Coding 共用启动时缓存的全局 Skills。

### `agent_open`

输入 `{}`，返回 `state,resumed,max_inflight,state_revision`。公共协议不暴露 `bridge_id`。如果 bridge 已存在则续租；如果 bridge 曾断开但仍有 active request，则创建新的内部 generation 并返回 `resumed=true`，随后同一 request 以原 `request_id` 恢复投递。

### `agent_exchange`

输入可同时提交 `responses`、`progress`、`stream_chunks` 与 `capacity`。response 可显式带 `event=tool_call|completion`；省略时 CWapi 根据 canonical completion 推导并保持兼容。纯 `progress/stream_chunks` 调用立即返回 `state=activity`，不进入 45 秒 bounded wait。

每个 returned request 除兼容 `state=claimed` 外，还带 `lifecycle_state`、`delivery`、`previous_state`、`resume_reason`、`last_activity`、`activity_deadline_at`、`hard_deadline_at`、request-scoped `progress/progress_at`、created/claimed/last-delivered 时间和 `event`。`delivery > 1` 永远表示相同 `request_id` 的 redelivery/resume，不是新任务。

生命周期至少包括：`QUEUED`、`CLAIMED`、`RUNNING`、`WAITING_TOOL`、`COMPLETED`、`FAILED_RETRYABLE`、`FAILED_FINAL`。heartbeat 只刷新 bridge lease；request activity 只由 delivery、progress、stream chunk 或 response 刷新，默认 activity timeout 为 3 分钟，并受默认 30 分钟不可延长 hard lifetime 限制。`progress` 是 request-scoped 可观察事件，不是 heartbeat 或 completion。

`results[].state` 保持 `completed/duplicate/rejected` 兼容。可恢复的 response/tool-call decode 错误返回 `error_detail`：`code,message,request_id,tool_call_id,tool_name,retryable`，request 转入 `FAILED_RETRYABLE` 并可用相同 ID 修正重投。一个坏 response 不回滚同批其他成功项。

`function.arguments` 可为 OpenAI JSON string 或 native JSON object。非流式路径 canonicalize 一次；流式 tool-call delta 必须先按 index 完整拼接 arguments，再只做一次 JSON parse，避免 double parse/double escape。Windows 路径、LF/CRLF、Unicode、引号、反斜杠和大文本均属于正式回归范围。

成功 tool call 进入 `WAITING_TOOL` 并立即释放本地 HTTP waiter，使本地软件执行工具并发起下一轮 OpenAI request。工具自身失败作为正常 `tool_result` 回到下一轮，不自动杀死 Agent task。只有结构化 completion 才表示当前 request 完成，不能搜索助手文本中的“完成”等词。

每个 output 带 `activity`，保留 `pending/inflight/active`，并包含 `queued_requests/active_requests/last_heartbeat_at/last_progress/image_bytes/requests[]`；`requests[]` 给出每个 active request 的 progress、last activity 与 hard deadline。`no_request` 的唯一含义仍是本次 bounded wait 内没有新的本地 OpenAI request。

bridge 生命周期与 request 生命周期分离。`agent_close` 只 detach 当前 bridge，保留 active request/history/delivery/last activity；重新 `agent_open` 后可继续同一 request。旧 bridge generation 的 operation 仍被拒绝，不能跨 generation 完成 request。新 OpenAI HTTP request 在 bridge 完全离线时仍快速失败 503，不进入等待队列。

### `agent_close`

输入 `{}`。关闭当前 bridge handle，但不删除 active request。没有 active bridge 时返回 `state=no_active_bridge`。

### `load_skill`

输入 `{"name":"debugging"}` 等 Skill ID，返回启动时缓存的 Skill name/description/content。Skill 文件修改后需重启 CWapi。缺失 Skill 返回 `SKILL_NOT_FOUND`。

## OpenAI-compatible Provider

鉴权：

```text
Authorization: Bearer <agent-api-key>
```

支持：

```text
GET  /v1/models
POST /v1/chat/completions
```

默认 model 为 `cwapi-web-gpt`。HTTP request body 上限 24 MiB，用于容纳 inline image 的 base64/JSON wire 开销；canonical broker JSON 与单次 stream 累积上限为 1 MiB。最大 active queue 16、默认 4 inflight；activity timeout 默认 3 分钟、hard request lifetime 默认 30 分钟、bridge lease 默认 2 分钟。Broker 原图常驻预算为 64 MiB，超过时新图片请求返回 429 `AGENT_MEDIA_BUSY`。没有 bridge 返回 503 `AGENT_BRIDGE_UNAVAILABLE`，queue 满返回 429 `AGENT_BUSY`，activity/hard timeout 返回 504 `AGENT_REQUEST_TIMEOUT`。

2.0.6 的 Provider 不把外部 JSON 直接交给 broker。正式转换链是：

```text
local Agent software
-> OpenAI Compatible Adapter
-> CWapi Canonical Format
-> Context Optimizer
-> MCP bridge
-> Web GPT
```

返回沿相反方向转换。Web GPT 是本地 Agent 软件实际使用且唯一负责推理的模型；本地软件只执行工具和本地操作，CWapi 不运行第二个 AI。

Canonical Format 表示 conversation/message/content part/tool definition/tool call/tool result/completion/error/stream chunk。图片以有序 `image_ref` part 与独立原始 bytes 关联；`system/developer/user/assistant/tool` role、`tool_call_id`、task/correlation metadata 会稳定保留。Context Optimizer 是确定性代码，只做 metadata/JSON tool result/安全去重，并直接 clone tool schema，不维护共享 tool-schema cache；不调用第二个 AI、不压缩用户任务语义。

`GET /v1/models` 的 model item 额外描述当前 adapter 名称与能力：`streaming/tools/parallel_tools/images=true`，`files=false`。图片能力只覆盖受限 inline raster image；普通文件能力仍关闭。错误边界使用稳定代码区分 request JSON/role/content、capability、canonical conversion、tool mapping、Web GPT response 和 stream conversion；普通客户端不会收到 Go stack。

Provider 接受标准顶层 `metadata` object（最多 32 项；key 最长 64 字符；string value 最长 512 字符，也允许 number/bool/null）并原样交付 Web GPT。建议长任务由本地 client 提供稳定 `task_id` 与 `correlation_id`；CWapi 仍以每个 HTTP request 的随机 `request_id` 做精确事务关联，不从消息文本推断 task 或 command lifecycle。

`stream=false` 返回 chat completion JSON；`stream=true` 保持 SSE keepalive，同时允许 Web GPT 通过 `agent_exchange.stream_chunks` 增量提交文本与 tool-call delta，Provider 会立即转发为 OpenAI-compatible SSE chunk。若未提交任何 stream chunk，则退回 buffered completion。只要提交过增量 chunks，最终 structured response 必须与所有已发 chunks 组装后的 canonical completion 一致，否则返回 retryable `AGENT_STREAM_FINAL_MISMATCH`，不会静默完成。

## File and media transport boundary

CWapi 2.0.6 当前只开放**图片传输**，不提供通用文件传输。

### Coding

- `coding_attachment` 只接受 active workspace 中的受限 raster image；
- MCP 只为图片发原生 `ImageContent`，不发普通文件 `EmbeddedResource`；
- 图片字节与 MIME 原样保留，不压缩、不缩放、不转码、不 OCR；
- source、Markdown、JSON、日志等可读文本继续通过 bounded `coding_exec` 读取。

### Agent

- 标准 Chat Completions `image_url` 仅接受 `data:` URI 的 inline raster image；远程 `http(s)` image URL 返回 `AGENT_IMAGE_URL_UNSUPPORTED`；
- 顶层 CWapi `attachments` 扩展只接受 inline image；non-image attachment 返回 `AGENT_IMAGE_ATTACHMENT_REQUIRED`；user 与 tool-result content 都可包含图片；
- Adapter 对外部 request 只做一次 JSON parse，并把图片位置编码成有序 canonical `image_ref`；Broker 入队时强制校验每个 ref 与原图 batch 精确匹配；
- 原始 bytes 仅保存在 request 内存生命周期中；同 ID redelivery 复用同一份 bytes，request 终态后立即释放；Broker-wide 原图预算 64 MiB；
- `agent_exchange` 返回 image metadata，并在 MCP tool result 追加原生 `ImageContent`；不产生 `EmbeddedResource`；
- 每 request 最多 8 张图片，单张最多 8 MiB、合计最多 16 MiB、图片单边最多 8192 px；SVG 明确拒绝。

base64 仅是 OpenAI-compatible JSON 与 MCP wire format 的传输封装，不改变图片编码格式。CWapi 不把 ChatGPT 会话上传反向写入 repository 或本地软件，也不提供任意文件系统传输、批量同步或长期附件存储。
