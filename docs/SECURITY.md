# CWapi 2.0.6 Security

## Trust boundaries

- MCP and Provider listeners bind only `127.0.0.1`；
- Coding/Agent routes use independent high-entropy bearer tokens；
- Provider uses an independent bearer API key with constant-time comparison；
- public HTTPS exposure, if needed by ChatGPT, is an explicit external deployment boundary；
- Coding creates an isolated empty CODEX_HOME per command and never reads native Codex identity/credentials。

## Coding

CWapi applies four explicit layers to the resolved executable/argv/CWD: Permanent Safety Guard, SAFE/FULL profile, optional capabilities, then execution. `internal/security` owns these boundaries; `internal/executionpolicy` is only a compatibility facade. Generated Codex rules remain defense in depth rather than the authoritative boundary.

The permanent guard is intentionally small. It rejects direct disk/boot tools (`format`, `diskpart`, `bcdedit`), automatic UAC elevation, execution or targeting of sensitive CWapi internals, untrusted Git executables, Git safety-ref publication, custom receive-pack injection and dangerous local/ext push transports. It canonicalizes existing path prefixes so symlink/junction redirection cannot escape these checks. It does not permanently protect broad user directories such as Downloads, Program Files or the Windows directory, and it does not pretend to understand arbitrary PowerShell/cmd/Node/Python script text.

SAFE maps to upstream Codex `workspaceWrite` and is intended to confine source read/edit/test to the owned workspace. It uses a synthetic command profile, isolated Git global/system configuration, disabled hooks and non-interactive credentials. Package/build caches live under `CWapi-data/runtime/workspaces/<workspace-hash>/cache` and survive commands; command Temp, bridge and short-lived state live under `CWapi-data/runtime/process/<process-id>` and are removed at termination. SAFE strips host GitHub identity and all CWapi/OpenAI/Codex internal credential variables. Network access is an independent, default-off capability.

FULL maps every command that passes the permanent guard to upstream `dangerFullAccess`. Its child environment begins with the current Windows user environment and removes CWapi/OpenAI/Codex internal secrets; normal PATH, user profile/AppData, SDK variables, Git global/system configuration, credential helpers, SSH, signing, hooks and package-manager configuration remain available. `taskkill`, `Stop-Process`, `Start-Process`, normal Git operations, `git -C`, credential plumbing and commands invoked inside scripts are not globally blacklisted. FULL therefore carries the ambient authority of the current user and is appropriate only after explicit operator authorization.

Network remains orthogonal to profile. SAFE + Network ON permits network use inside the SAFE sandbox; FULL + Network ON permits normal developer network use. Remote Git Rewrite is a second independent capability and defaults OFF. OFF rejects direct force, force-with-lease, delete and deletion-refspec push forms. ON adds those forms but does not bypass unsafe transports, receive-pack injection, safety-ref publication, trusted-Git checks, CWapi self-protection or the no-auto-elevation rule.

Before direct local Git operations likely to discard content, CWapi records HEAD/branch/dirty/timestamp and creates a bounded `refs/cwapi/safety/<id>` recovery ref when a commit object can represent the state. At most 32 recovery refs and metadata entries are retained per workspace. Normal push cannot publish this namespace.

SAFE/FULL are the complete Coding access profiles; CWapi issues no reusable elevation credential. The Git workspace stays on a local tracking branch when prepared, and dirty/local/diverged state is never reset implicitly. Commands launched by a FULL shell script are governed by OS/user authority rather than partial shell parsing; SAFE containment is provided by the upstream sandbox plus verifiable outer checks.

The Coding MCP public surface uses `repository_url` as stable identity and does not expose the random internal session ID. CWapi still keeps that internal ID and exact repository ownership mapping for cancellation, close races and stale-operation protection.

Coding MCP exposes image-only `coding_attachment`. It reads only bounded raster images inside the selected active workspace, using the same optional target-ref routing rules as the other branch-aware Coding tools, rejects traversal/Git metadata/non-image inputs, and emits native `ImageContent` with original bytes/MIME. Ordinary files are not emitted as MCP resources; workspace text is read through bounded `coding_exec` output.

Workspace maintenance resolves targets under the managed root and is available only from the local Desktop confirmation flow.

CWapi-owned GitHub CLI identity uses `CWapi-data/auth/github` for every Coding workspace. This is not stored in a repository or portable package. Token material should remain in Windows Credential Manager/keyring when supported; CWapi neither copies it into `cwapi.json` nor exposes it through Coding MCP.

## Agent

- HTTP request body hard limit 1 MiB；
- active queue hard limit 16；
- internal bridge IDs remain random and generation-scoped; public `request_id` remains random and exact-correlated；
- `request_id` is transaction-only and is never promoted to third-party command/session identity；optional `metadata.task_id` / `metadata.correlation_id` is bounded and client-supplied；
- request timeout/disconnect terminalize request state；bridge close/stale detaches active requests for same-ID resume rather than deleting them；
- runtime-owned heartbeat maintains bridge liveness only；request activity requires delivery/progress/stream/response and is capped by a non-extendable hard lifetime；
- retryable tool-call decode/mapping errors expose structured request/tool identity and remain recoverable；
- malformed messages/tools/responses are rejected before delivery；
- exchange activity reports only broker revision and queue truth；`no_request` never asserts external process state；
- top-level CWapi `attachments` accepts only bounded inline raster images; non-image items return `AGENT_IMAGE_ATTACHMENT_REQUIRED`；
- `image_url` accepts only bounded `data:` raster images; remote URLs return `AGENT_IMAGE_URL_UNSUPPORTED` and other unsupported non-text parts remain rejected；
- Agent MCP emits native `ImageContent` only for validated raster images with exact `image_ref` binding, never generic-file `EmbeddedResource`; raw image bytes are request-scoped under a 64 MiB broker-wide budget and ChatGPT conversation uploads are not imported into local software；
- normal observability stores metadata only, not full conversation payloads。

Server Instructions may guide workflow and efficiency, but they are not a security boundary. Authorization, input validation, request correlation, duplicate handling and terminal-state rules remain enforced by Go code.

## Config and package

Config contains local bearer secrets and must remain inside adjacent `CWapi-data`. It is never copied into candidate stage. Runtime archives/executables are SHA-256 pinned; package audit scans names and content for user data, credentials, logs, machine identity and absolute build paths.

Tunnel Runtime API keys are stored in Windows Credential Manager and injected only into their matching tunnel-client child process environment. They are not written into `cwapi.json` or generated Tunnel profiles.

## Reporting

Security issues should include affected version/commit, exact local route or component, reproduction, impact, and whether the issue requires SAFE or FULL. Do not include real tokens or credentials in reports.
