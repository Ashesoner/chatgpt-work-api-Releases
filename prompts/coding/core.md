# CWapi Coding Core Protocol

CWapi Coding mode lets Web GPT operate durable Git workspaces through MCP. Workspace identity is the canonical repository plus canonical `target_ref`, so different branches of the same repository may have independent active Coding sessions.

## Protocol

- Start or resume a workspace with `coding_open(repository_url, target_ref, ..., resume=true)`. `target_ref` is required for `coding_open`.
- Use the same canonical `repository_url` and the same branch `target_ref` for the task. CWapi owns the internal session handle; Web GPT does not receive or store a session ID. Different `target_ref` values for one repository identify independent workspaces/sessions.
- `expected_commit` is an exact guard for the resolved target/baseline used to open the workspace. On a resumed durable workspace, the returned current branch, current HEAD and dirty state are the current Git truth and may legitimately differ from that original baseline.
- Execute one exact local development command with `coding_exec`. Pass the executable in `command` and each argument separately in `argv`; do not encode a shell command line when direct argv is sufficient. Use foreground `run` for bounded work. For commands expected to outlive a normal MCP call, use `action=start`, then poll `action=status` with the returned `stdout_cursor` / `stderr_cursor` so each call receives only new output; stop polling at a terminal state.
- `coding_exec`, `coding_status`, `coding_attachment`, and `coding_close` accept optional `target_ref`. If exactly one branch of a repository is active, repository-only calls remain compatible. If multiple branches are active, always carry the current conversation matching `target_ref`; omitting it returns `CODING_SESSION_AMBIGUOUS`. Naming a non-active `target_ref` returns `CODING_SESSION_NOT_ACTIVE`; CWapi never falls back to another branch.
- One workspace (`repository_url + target_ref`) has one foreground Coding operation at a time. Do not intentionally overlap operations for the same workspace; independent branches may operate independently.
- Use `coding_status` only when repository/workspace truth is needed. It refreshes local Git truth when idle and reports summaries of active persistent processes without argv. When multiple branches of the same repository are active, include the matching `target_ref`. If it reports `state=busy`, use its active command/start/elapsed metadata to distinguish a genuine long-running foreground command from stale state; do not infer failure from `busy` alone.
- Use `coding_attachment(repository_url, target_ref?, paths)` only for raster images already inside the selected active workspace. It returns native MCP `ImageContent` with the original image bytes and MIME type; CWapi does not recompress, resize, transcode or OCR images. Ordinary files are not transferred; repository text remains inspected and changed through repository commands.
- Close the selected workspace session with `coding_close(repository_url, target_ref?)` after the task is genuinely finished. When multiple branches of the same repository are active, include the matching `target_ref`. Closing does not reset Git or delete durable workspace data.
- Coding mode directly executes repository-scoped development commands through CWapi; it is not the Agent mode OpenAI tool-call relay.

## Skills

- `load_skill(name)` loads one startup-cached global Skill by its Skill ID.
- Load only Skills listed in the startup Skill inventory. A missing Skill returns `SKILL_NOT_FOUND`.
- Loading a Skill does not modify the repository or workspace.

Tool results are authoritative for command exit status and output. Returned workspace/Git state is authoritative for the state represented by that result. Protocol and safety enforcement are implemented by CWapi, not by natural-language conventions.
