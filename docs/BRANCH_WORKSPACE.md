# Branch-aware Workspace V1

> Baseline: upstream CWapi 2.0.5. This document records the design, user workflow, upgrade notes, compatibility boundaries, and custom changes for this fork.

## 1. Goal

CWapi 2.0.5 currently treats one Git repository as one durable workspace. This prevents multiple ChatGPT conversations from working on different branches of the same repository at the same time.

V1 changes workspace identity from repository-only to repository + canonical target ref, while preserving existing behavior for the same repository + same branch.

Target behavior:

- same repository + different branches: allowed concurrently;
- same repository + same branch: keep existing busy/resume behavior;
- different repositories: keep existing behavior;
- no change to Agent, Tunnel, coding_exec execution semantics, SAFE/FULL, Network Access, Remote Git Rewrite, Skills, Git safety policy, or Codex ToolHost; `coding_exec`, `coding_status`, and `coding_close` only gain an optional `target_ref` selector.

## 2. Workspace identity

Current conceptual identity:

```text
repository
```

V1 conceptual identity:

```text
normalized repository + canonical target ref
```

Examples:

```text
ashesoner/bilibili_v_summary + refs/heads/blockgl
ashesoner/bilibili_v_summary + refs/heads/route-checker
ashesoner/bilibili_v_summary + refs/heads/lightweight-timeline
```

All workspace-related components must use one shared identity/key helper. Directory selection, Coding active/opening ownership, maintenance, and GUI lookup must not implement separate key rules.

## 3. Coding concurrency and routing rules

- `repo + branch-a` and `repo + branch-b` may be active at the same time.
- a second independent open of `repo + branch-a` keeps the original BUSY/resume semantics.
- open/exec/status/close/cleanup/resume all use the same branch-aware `WorkspaceIdentity` and `WorkspaceKey`.
- `coding_exec`, `coding_status`, and `coding_close` accept optional `target_ref`.
- when `target_ref` is present, it is canonicalized with the same rule as workspace identity and routes exactly to that active branch. Missing/non-active target refs return `CODING_SESSION_NOT_ACTIVE`; they never fall back to another branch.
- when `target_ref` is omitted and the repository has exactly one active branch, the original repository-only behavior is preserved.
- when `target_ref` is omitted and the repository has more than one active branch, routing returns `CODING_SESSION_AMBIGUOUS`; no newest/oldest/arbitrary branch is selected.
- MCP remains stateless and no public session ID is introduced.

All uses of Coding `active`, `opening`, `finalizeClose`, resume, lookup, and cleanup ownership must use the same branch-aware identity rule to avoid stale BUSY entries or deleting another branch session's ownership.

## 4. Workspace GUI

The existing durable workspace management dialog is extended instead of adding a second executable.

Each entry displays:

```text
owner/repository
Branch: branch-name
```

Actions:

- Open Folder: open that workspace's `repo` directory in the OS file manager.
- Delete and Rebuild: target only that repository + branch workspace.

The existing repository summary/index fields remain available for compatibility:

```text
RepositoryCount
Repositories []string
InvalidEntries
```

The index additionally exposes a structured list whose entries contain only:

```text
repository
target_ref
branch
```

Local workspace paths and hash/container names are intentionally not exposed to the frontend. The GUI sends only repository + target ref back to the backend, so the V1 path-shortening change requires no frontend path construction.

Open Folder and Delete/Rebuild resolve the physical workspace on the backend. Resolution order is: the new short branch-aware directory key, the original V1 64-hex branch-aware key, then the upstream 2.0.5 repository-only 64-hex key. Every reusable container must have `workspace.json` metadata matching both `repository` and canonical `target_ref` exactly; a repository match alone is never sufficient and no request falls back to another branch.

V1 intentionally keeps the existing conservative maintenance lock: destructive workspace maintenance remains blocked while Coding activity is present. Branch-scoped maintenance locking can be considered later. Open Folder is non-destructive but is serialized with desktop maintenance so it cannot race a delete operation.

## 5. Old workspace compatibility

V1 does not automatically rename or migrate either original V1 64-hex branch-aware directories or upstream 2.0.5 repository-only workspace directories.

Reason: an old workspace may contain uncommitted work, local commits, runtime state, or metadata. Automatic migration adds risk; compatibility is implemented by exact metadata-validated reuse instead.

Upgrade behavior:

1. back up the existing `CWapi-data` directory before switching versions;
2. keep the old CWapi installation available for rollback;
3. start the modified build only after the original CWapi process has exited;
4. new branches use the short branch-aware workspace directory format;
5. existing original-V1 64-hex or upstream repository-only directories are reused in place only when metadata exactly identifies the requested repository + target ref;
6. old directories are never renamed or migrated automatically and remain manageable through the same backend resolver.

## 6. Original CWapi and modified CWapi conflict prevention

CWapi continues to use Wails `SingleInstanceLock` with the existing normal/probe instance IDs. V1 does not add process scanning, a second mutex/file lock, named mutex, custom IPC, preflight process checks, or an explicit quit path for the second process.

The formal V1 target is: **guarantee single-instance conflict isolation; when a V1 build is already the primary instance, provide an explicit WarningDialog. If an original 2.0.5 build is already the primary instance, a later V1 launch is still rejected by Wails, but the V1 warning is not guaranteed.**

When a V1 build is already running at the same Windows privilege level, Wails owns the second-instance rejection and the V1 primary receives `OnSecondInstanceLaunch`, restores/shows its main window, and displays a Wails `runtime.MessageDialog` warning:

```text
CWapi 已在运行

检测到另一个 CWapi 启动请求。
为避免端口、Tunnel、运行时状态和工作区冲突，
CWapi 只允许同时运行一个实例。
请先退出当前 CWapi，再启动另一个版本。
```

`CWAPI_GUI_PROBE_CONFIG` continues to select the independent probe instance ID, so GUI probe behavior remains isolated from the normal CWapi instance ID.

D0 executable integration verified `original 2.0.5 primary -> V1 second`: the V1 process was rejected without starting a second MCP/service, Tunnel client, or listener. No V1 WarningDialog is required in this direction because the already-running original process owns the second-instance callback and does not contain the V1 warning logic.

Windows/Wails single-instance notification can have a boundary between processes running at different privilege/elevation levels. V1 does not replace Wails IPC to bridge that boundary; same-privilege scenarios are part of final EXE integration testing, while mixed-privilege behavior is documented as a known platform/framework limitation.

## 7. Development workflow

Development may be performed by the currently working original CWapi against this fork's cloned workspace. Editing source code in the clone does not modify the running CWapi executable.

Recommended order:

1. update this design document and the test document;
2. implement shared branch-aware workspace identity;
3. update workspace manager paths;
4. update Coding active/opening/session cleanup keys;
5. add/adjust tests;
6. extend workspace index and maintenance APIs;
7. extend GUI;
8. add startup conflict message behavior;
9. run regression tests;
10. build the modified executable;
11. only then stop the original CWapi and perform real executable integration tests.

## 8. User workflow after V1

Example ChatGPT/CWapi usage:

Conversation A:

```text
coding_open(repository_url=<same repo>, target_ref=blockgl, resume=false)
```

Conversation B:

```text
coding_open(repository_url=<same repo>, target_ref=route-checker, resume=false)
```

Conversation C:

```text
coding_open(repository_url=<same repo>, target_ref=lightweight-timeline, resume=false)
```

Expected result: each branch has a separate durable workspace and can be worked on concurrently.

After more than one branch of the same repository is active, branch-specific operations use the optional selector:

```text
coding_exec(repository_url=<same repo>, target_ref=blockgl, ...)
coding_status(repository_url=<same repo>, target_ref=route-checker)
coding_close(repository_url=<same repo>, target_ref=lightweight-timeline)
```

For backward compatibility, omitting `target_ref` still works while that repository has exactly one active branch. If multiple branches are active, repository-only exec/status/close return `CODING_SESSION_AMBIGUOUS`. Opening another independent session for the same repository + same branch remains blocked or resumed according to existing CWapi semantics.

## 9. Upgrade checklist from upstream/original 2.0.5

Before switching:

```text
[ ] Commit/preserve important project work.
[ ] Close active Coding sessions where practical.
[ ] Exit the original CWapi before launching the modified build.
[ ] Back up the complete CWapi-data directory.
[ ] Keep the original CWapi installation unchanged for rollback.
```

First modified-build run:

```text
[ ] Verify Coding and Agent startup state.
[ ] Verify existing Tunnel configuration/state as documented for the build.
[ ] Use a test repository first.
[ ] Open two different branches of the same repository in different conversations.
[ ] Confirm workspace paths differ and current branches are correct.
[ ] Confirm same-branch duplicate open still follows BUSY/resume behavior.
[ ] Verify the workspace management GUI entries and Open Folder action.
```


Rollback:

1. exit the modified CWapi;
2. restore/use the untouched original CWapi installation;
3. if required, restore the backed-up original `CWapi-data` directory;
4. do not delete old workspaces until the modified build has been fully accepted.

## 10. Troubleshooting focus

When branch-aware behavior fails, check in this order:

1. repository URL normalization;
2. canonical target ref;
3. generated branch-aware workspace identity/key;
4. workspace metadata `repository` and `target_ref`;
5. actual Git current branch;
6. Coding `active` / `opening` owner key;
7. session close/cleanup removing the correct key;
8. GUI entry referring to the same identity used by backend maintenance.

## 11. Windows path-length mitigation

Windows testing of a pre-shortening build reproduced `Filename too long` during a SAFE Go module download under `CWapi-data/runtime/workspaces/<64-hex>/cache/go-mod/...`. That established a concrete long-path risk for the 64-hex runtime layout; the durable workspace directory also contributed unnecessary depth.

The V1 fix separates logical identity from on-disk naming:

- Coding active/opening/close ownership keeps the full repository + target-ref identity key;
- new durable workspace directories use a stable 24-hex (96-bit) prefix derived from the full branch-aware SHA-256 digest;
- new SAFE runtime workspace/cache directories use the same 24-hex length policy;
- original V1 64-hex branch-aware workspaces and upstream 2.0.5 repository-only 64-hex workspaces remain usable in place;
- existing 64-hex runtime cache directories are left in place but are not reused for new commands; new SAFE commands always use the short runtime root, and Delete/Rebuild cleans both short and legacy runtime roots;
- no workspace or runtime directory is automatically renamed or migrated;
- `workspace.json` remains authoritative for repository + target-ref identity, and metadata mismatch is rejected.

Unit tests cover short-key stability, branch separation, metadata mismatch rejection, original-V1 64-hex compatibility, repository-only legacy compatibility, short runtime-cache selection, and cleanup of both short and legacy runtime roots. Runtime integration was then verified on the 2026-09-18 01:25 V1 build: SAFE injected a 24-hex runtime workspace ID and `go test ./...` passed without any test-only `GOMODCACHE`/`GOCACHE` override, so the previously observed deep-cache `Filename too long` failure did not reproduce.

## 12. Custom changes relative to upstream 2.0.5

Implemented V1 changes in this fork:

1. branch-aware durable workspace identity;
2. concurrent Coding sessions for different branches of the same repository;
3. preserve same-branch BUSY/resume behavior;
4. branch-aware workspace index details while preserving repository summary fields;
5. branch display in the existing workspace management GUI;
6. backend-resolved Open Folder action without exposing local workspace paths to the frontend;
7. branch-scoped Delete and Rebuild with exact legacy metadata fallback;
8. visible Open Folder launching (Explorer is not routed through the hidden background-process helper);
9. short durable/runtime workspace IDs with original 64-hex compatibility and no automatic migration;
10. single-instance conflict isolation, with an explicit startup WarningDialog when a V1 build is the already-running primary;
11. tests and upgrade/use documentation.


## 13. Release documentation status

At V1 validation closeout, the public Coding/workspace documentation is aligned to the V1 contract:

- `coding_open` selects repository + target ref;
- `coding_exec`, `coding_status`, and `coding_close` accept optional `target_ref`;
- repository-only follow-up calls remain compatible with exactly one active branch, return `CODING_SESSION_AMBIGUOUS` with multiple active branches, and an explicitly named inactive target returns `CODING_SESSION_NOT_ACTIVE` without fallback;
- user examples show two branches of one repository active concurrently and carry the corresponding target ref through follow-up calls;
- the existing GUI manages repository + branch entries with backend-resolved Open Folder and branch-scoped Delete/Rebuild;
- original V1 64-hex branch-aware and upstream repository-only workspaces are not auto-migrated; both are reused only when metadata exactly matches the requested identity;
- the original 2.0.5 upgrade/rollback procedure requires a pre-upgrade `CWapi-data` backup and keeping the original build available;
- same-privilege duplicate launches use Wails single-instance isolation; a V1 primary handles the callback and shows the explicit warning, while an original 2.0.5 primary may reject the V1 second process without showing the V1 warning; mixed Windows privilege levels remain a documented Wails/Windows boundary;
- new durable and SAFE runtime workspace/cache directories use short stable IDs; old 64-hex durable workspace directories remain compatible in place without migration, while old 64-hex runtime caches are no longer selected for new commands. The running 2026-09-18 01:25 V1 build verified the SAFE runtime path uses 24 hex and the formerly failing Go test path now passes without overrides; a newly opened branch was also user-verified to create the 24-hex durable workspace directory `26ef383d26f7b9b3baed3f05`.

Runtime Coding prompt text under `prompts/` has now been reviewed separately and aligned to the same branch-aware routing contract.

## 14. Runtime prompt and frontend audit status

`prompts/coding/core.md` is aligned to V1: workspace identity is repository + canonical target ref; different branches may have independent active sessions; one selected workspace allows one foreground Coding operation at a time; `coding_open` requires `target_ref`; and exec/status/close use the same optional target selector rules as the implementation. When multiple branches of one repository are active, Web GPT is explicitly instructed to keep sending the current conversation's matching `target_ref`. No public `session_id` is introduced. Other Coding runtime prompts were searched and no additional repository-only ownership rule required changes.

Frontend dependency audit was performed without modifying dependency manifests or running an automatic fix. The lockfile installs `vite@7.0.0`, `vitest@3.2.4`, and transitive `@vitest/mocker@3.2.4` as development dependencies. Full `npm audit` reports three vulnerable package entries: Vite (high), Vitest (critical aggregate), and `@vitest/mocker` (moderate). `npm audit --omit=dev` reports zero vulnerabilities, so these packages are absent from the production dependency set embedded into the CWapi runtime; they affect local build/test/dev tooling instead.

Remediation classification:

- Vite: npm audit recommends `7.3.6`; this stays within major 7 and is not a semver-major upgrade.
- Vitest critical UI-server advisory: patched in `3.2.6`; npm audit recommends `3.2.7`, which is a non-major upgrade from 3.2.4.
- Vitest / `@vitest/mocker` redirect-mock advisory: upstream marks versions before `4.1.11` as affected and states older 3.x is not planned to receive the fix. Therefore fully clearing the current Vitest-family audit requires moving the Vitest line to at least `4.1.11`, which is a major/breaking-version upgrade from 3.2.4. npm audit's `3.2.7` direct-package suggestion only addresses the older critical Vitest issue; it still resolves `@vitest/mocker@3.2.7`, which remains inside the newer advisory's affected range.

Because `npm audit --omit=dev` is clean and CWapi embeds built `frontend/dist` rather than Node development tooling, these findings do **not block a V1 test build**. They remain a development-toolchain security follow-up, particularly if Vite/Vitest development servers are exposed beyond localhost. No dependency update is performed in V1 C.5.
