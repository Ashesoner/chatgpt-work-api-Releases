# Branch-aware Workspace V1 Test Plan

This file records the test plan, execution steps, expected results, and final acceptance results for the branch-aware workspace change.

## 1. Test principle

Development and most tests are performed through the currently working original CWapi against the fork's cloned source workspace. The original running CWapi executable is not replaced during source-level testing.

The modified executable is launched only in integration stages. Normal-instance tests that collide with the running original require the original CWapi to be exited. D1 may instead use the independent probe instance ID together with an isolated `CWAPI_V2_CONFIG` data root and non-production ports so probe testing cannot touch production runtime state.

Tests must never point temporary test data at the user's production `CWapi-data` directory.

## 2. Stage A — Source and unit tests

Original CWapi may remain running.

Required checks:

1. `go test ./...`
2. workspace identity differs for the same repository on different canonical target refs;
3. workspace identity is stable for the same repository + same canonical target ref;
4. branch-aware workspace paths differ for branch A and branch B;
5. existing different-repository behavior remains valid;
6. temporary workspace tests use isolated temp directories.

Expected:

```text
repo + branch-a != repo + branch-b
repo + branch-a == repo + branch-a
```

## 3. Stage B — Coding service concurrency tests

Original CWapi may remain running.

Required cases:

A. `repo + branch-a` and `repo + branch-b` can remain active concurrently;
B. `coding_exec(target_ref=branch-a)` executes only in branch-a;
C. `coding_exec(target_ref=branch-b)` executes only in branch-b;
D. `coding_status(target_ref=...)` returns the selected branch;
E. `coding_close(target_ref=branch-a)` closes only branch-a and leaves branch-b active;
F. repository-only exec/status/close preserve original behavior when exactly one branch is active;
G. repository-only exec/status/close return `CODING_SESSION_AMBIGUOUS` when multiple branches are active;
H. a non-active `target_ref` returns `CODING_SESSION_NOT_ACTIVE` and never falls back to another branch;
I. same-repository + same-branch BUSY/resume behavior remains unchanged.

Additional lifecycle checks:

- branch-aware workspace paths differ for branch A and branch B;
- session cleanup removes only the matching branch-aware `active`/`opening` entry;
- closing all sessions leaves no stale BUSY state.

Implementation review requirement:

Search all uses of:

```text
active[
opening[
finalizeClose
lookupActiveLocked
WorkspaceKey
```

and verify they use the same shared branch-aware identity rule where workspace ownership is intended.

## 4. Stage C — Build and GUI compile tests

Original CWapi may remain running.

Required checks:

1. Go build succeeds;
2. frontend build succeeds;
3. generated/bound frontend types compile;
4. old `RepositoryCount` / `Repositories` / `InvalidEntries` summary fields remain compatible;
5. structured `Workspaces[]` entries expose only repository, target_ref, and branch;
6. index lists two branches of the same repository separately and accepts valid legacy metadata;
7. invalid metadata does not enter the normal workspace list;
8. branch-scoped delete leaves sibling branches untouched;
9. missing target refs do not fall back to another branch;
10. legacy resolution requires exact repository + target_ref metadata;
11. Open Folder resolution returns the correct backend repo path without exposing path/hash in the frontend model;
12. the existing workspace manager renders repository + branch with Open Folder and Delete/Rebuild actions;
13. no unrelated screen or service requires code changes.

The modified executable has now been used for V1 integration validation. The steps below are retained as the original Stage D procedure for future reruns.

## 5. Stage D — Real executable integration tests

Before starting:

```text
[ ] Preserve/commit source changes.
[ ] Close active development Coding sessions as appropriate.
[ ] Exit the original CWapi completely.
[ ] Confirm the modified build is the only CWapi instance to be launched.
```

### D1. Single-instance conflict protection

Final executable integration cases (not run during source/unit Stage B):

1. normal-permission CWapi running -> launch another normal-permission CWapi;
2. administrator CWapi running -> launch another administrator CWapi;
3. mixed privilege/elevation levels -> record behavior as the known Wails/Windows single-instance callback boundary; V1 does not add custom IPC to bridge it.

Expected for same-privilege cases:

- Wails `SingleInstanceLock` prevents the second instance from continuing as an independent CWapi instance;
- when a V1 build is the already-running primary, that V1 process receives `OnSecondInstanceLaunch`, restores/shows its existing window, and displays the documented `CWapi 已在运行` Warning `runtime.MessageDialog`;
- when an original 2.0.5 build is the already-running primary and V1 is launched second, Wails still rejects the V1 process, but no V1 WarningDialog is guaranteed because the old primary owns the callback and does not contain the V1 warning implementation;
- in either direction, rejection must prevent a second MCP/service/Tunnel/listener set from becoming active;
- the V1 callback does not restart V2 service or otherwise modify normal startup/shutdown/tray behavior.

### D2. Original feature regression

Verify at minimum:

- Coding startup;
- Agent startup;
- Coding Tunnel;
- Agent Tunnel;
- SAFE mode;
- FULL mode;
- Network Access toggle;
- Remote Git Rewrite toggle;
- Skills loading;
- ordinary Coding operation across different repositories;
- `resume` behavior;
- `coding_close` behavior.

No regression is acceptable in these areas for V1 acceptance.

### D3. Same repository, different branches

Use a dedicated test repository when possible, containing at least:

```text
main
branch-a
branch-b
```

Use separate ChatGPT conversations:

```text
Conversation A -> main
Conversation B -> branch-a
Conversation C -> branch-b
```

Expected:

- all three can remain active concurrently;
- workspace repo paths are different;
- each Git current branch is correct;
- a file changed in one workspace is not visible as an uncommitted change in another workspace;
- commands execute independently.

### D4. Same repository, same branch protection

Attempt another independent open of an already active branch.

Expected:

- no second independent workspace is created;
- original BUSY/resume semantics are preserved.

### D5. Workspace management GUI

Expected entries:

```text
owner/repository
Branch: main

author/repository
Branch: branch-a

author/repository
Branch: branch-b
```

For each entry:

- Open Folder opens the correct `<workspace>/repo` directory;
- displayed branch matches Git state;
- entries for different branches of the same repository are not collapsed into one row.

### D6. Delete and Rebuild

Precondition: no Coding activity, preserving V1's existing conservative maintenance lock.

Delete only `repo/branch-b`.

Expected:

- branch-b workspace is removed;
- main workspace remains;
- branch-a workspace remains;
- reopening branch-b recreates only its branch-aware workspace.

## 6. Upgrade test from original 2.0.5

Use a copy/backup of existing data for this test, never the only production copy.

Procedure:

1. preserve original installation;
2. back up complete `CWapi-data`;
3. exit original CWapi;
4. launch modified build;
5. verify existing configuration/tunnel behavior;
6. open a known repository branch;
7. verify branch-aware workspace creation;
8. confirm original V1 64-hex and upstream repository-only workspace directories are not automatically renamed or deleted;
9. verify rollback by exiting modified build and reopening original CWapi with preserved original data if needed.

## 7. Path-length regression

Windows testing of a pre-shortening build reproduced a concrete failure inside the SAFE runtime cache:

```text
CWapi-data/runtime/workspaces/<64-hex>/cache/go-mod/cache/vcs/<64-hex>/hooks/
-> Filename too long
```

V1 therefore shortens new durable workspace directory IDs and runtime workspace/cache IDs to 24 hex characters while keeping logical Coding ownership on the full repository + target-ref identity. Original V1 64-hex branch-aware workspaces and upstream repository-only 64-hex workspaces remain compatible without automatic rename/migration. Existing 64-hex runtime cache directories may remain on disk, but new commands must use the short runtime root.

Source/unit coverage must verify:

- same identity -> same short ID;
- same repository + different branches -> different short IDs;
- metadata mismatch -> reject;
- original V1 64-hex branch-aware directory -> recognized;
- upstream repository-only 64-hex directory -> recognized only on exact metadata match;
- existing 64-hex runtime cache -> not selected for new commands; short runtime root is used instead, and Delete/Rebuild cleans both short and old runtime roots;
- new SAFE cache environment points at the short runtime root.

Runtime EXE regression is verified on the running 2026-09-18 01:25 V1 build: SAFE exposes a 24-hex runtime workspace ID and `go test ./...` passes without cache overrides. A newly opened branch was also user-verified to create the 24-hex durable workspace directory `26ef383d26f7b9b3baed3f05`.

## 8. Acceptance criteria

V1 closeout status is recorded below. `[x]` means verified by automated/source checks or real V1 use; `[~]` means explicitly waived as non-blocking and must not be read as a performed test:

```text
[x] Same repository + different branches can work concurrently.
[x] Same repository + same branch keeps existing BUSY/resume behavior.
[x] Workspace paths are branch-specific and new durable/runtime workspace IDs are short; a newly opened branch created durable ID 26ef383d26f7b9b3baed3f05 and SAFE runtime uses 24 hex.
[x] Original V1 64-hex and upstream repository-only legacy directories remain compatible without automatic migration.
[x] Close/cleanup does not leave stale BUSY entries.
[x] GUI lists repository + branch separately.
[x] Open Folder targets the correct workspace.
[x] Delete and Rebuild affects only the selected branch workspace (source/unit coverage).
[x] No V1 regression was found in the exercised Coding/Agent/Tunnel/security controls; automated/source checks and ongoing V1 use remain the evidence, not an exhaustive platform certification.
[~] Additional V1-primary normal/admin duplicate-launch UI testing was explicitly waived for V1 closeout. Existing source/unit coverage and the completed original-2.0.5-primary -> V1-second isolation test remain recorded below.
[x] Upgrade and rollback steps are documented and user-verified with the V1 executable and the required prompts/coding files.
```

## 9. Test results

Fill this section during implementation.

### Source/unit

- Status: PASS
- Notes:
  - Go 1.25.0 Windows amd64 is installed. The already-running CWapi process has the pre-install PATH, so tests were invoked through `C:/Program Files/Go/bin/go.exe`.
  - Branch-aware `WorkspaceIdentity` / `WorkspaceKey` implementation and unit tests are present.
  - `go test ./...` passes on the running 2026-09-18 01:25 V1 build without test-only `GOMODCACHE` / `GOCACHE` overrides. Direct environment inspection shows SAFE uses a 24-hex runtime workspace ID, and the previously observed deep-cache `Filename too long` failure does not reproduce.
  - `git diff --check` passes.

### Coding concurrency

- Status: PASS (source/unit stage)
- Notes:
  - `coding_open` ownership is branch-aware: different branches can hold separate active/opening entries; the same repository + same canonical branch retains BUSY/resume semantics.
  - `coding_exec`, `coding_status`, and `coding_close` now accept optional `target_ref` and route through the same canonical `WorkspaceIdentity` / `WorkspaceKey` rule.
  - Exact target routing was verified independently for branch-a and branch-b. Closing branch-a leaves branch-b active.
  - Repository-only calls remain compatible when exactly one branch is active and return `CODING_SESSION_AMBIGUOUS` when multiple branches are active.
  - A non-active `target_ref` returns `CODING_SESSION_NOT_ACTIVE` and does not fall back to another branch.
  - Coding ownership remains keyed by the full repository + canonical target-ref identity; only on-disk directory IDs are shortened.

### Build/GUI

- Status: PASS (source/build stage)
- Notes:
  - Workspace Index now preserves the repository summary fields and adds `Workspaces[]` entries containing only `repository`, `target_ref`, and `branch`.
  - Unit tests cover same-repository multi-branch listing, legacy index compatibility, invalid metadata exclusion, branch-scoped deletion, missing-target no-fallback behavior, exact legacy metadata matching, and backend repo-path resolution.
  - `DeleteWorkspace(repository,targetRef)` preserves the existing global Coding/Agent maintenance-busy policy.
  - `OpenWorkspaceFolder(repository,targetRef)` resolves the physical workspace on the backend and launches `explorer.exe` with standard visible `os/exec`; the frontend never receives or constructs workspace paths/hashes. The running 2026-09-18 01:25 V1 build was user-verified to open the workspace folder visibly from the GUI.
  - The existing workspace-management overlay now shows repository + branch and provides Open Folder / Delete and Rebuild actions. No second window/program was added.
  - `go test ./...` passes.
  - Frontend production build passes (`npm ci --ignore-scripts` followed by `npm run build`). `--ignore-scripts` was needed because the running CWapi SAFE sandbox blocks npm install lifecycle child-process spawning; this did not change dependency versions or tracked package metadata.
  - `git diff --check` passes.
  - Single-instance/startup behavior and real executable integration were not started in this stage.

### Single-instance source/unit

- Status: PASS (source/unit stage)
- Notes:
  - `cwapiSingleInstanceID` remains `007f7623-c85d-481d-be63-7e6887667f4c`.
  - `cwapiProbeSingleInstanceID` remains `90e9bd2a-3834-4ba4-95e8-9f4683543610`, and `CWAPI_GUI_PROBE_CONFIG` continues to select it independently.
  - `applicationOptions` still configures Wails `SingleInstanceLock` and the existing `OnSecondInstanceLaunch` callback.
  - The callback only reads the existing Wails context, unminimises/shows the main window, and invokes a Warning `runtime.MessageDialog` with the documented conflict text. Unit tests verify that service/config/startup state are not replaced or restarted.
  - No process scan, custom mutex/file lock, custom IPC, explicit second-instance `Quit`, tray change, `HideWindowOnClose` change, shutdown change, or V2 service restart was added.
  - Additional V1-primary normal->normal and administrator->administrator executable tests were explicitly waived for V1 closeout. They are not reported as PASS. Mixed privilege/elevation behavior remains the documented Wails/Windows callback boundary and is not reimplemented in V1.


### Documentation/release consistency

- Status: PASS (documentation/source consistency stage)
- Notes:
  - README, protocol, architecture/workflow, Coding Guide, Getting Started, FAQ, Troubleshooting, Operations, GUI, migration/version path examples, and the branch-workspace documents were audited for repository-only assumptions.
  - Public docs now describe repository + canonical target ref workspace identity, optional target selectors on exec/status/close, single-branch compatibility, multi-branch ambiguity, explicit-target not-active behavior, and no cross-branch fallback.
  - English and Chinese user guides include same-repository branch-a/branch-b examples with target-aware follow-up calls.
  - GUI Open Folder / branch-scoped Delete-Rebuild, exact legacy metadata fallback, no automatic legacy migration, short durable/runtime path compatibility, 2.0.5 upgrade/rollback, single-instance conflict isolation, V1-primary WarningDialog behavior, the old-primary cross-version warning limitation, and the mixed-privilege Wails/Windows boundary are documented.
  - No new executable integration test was run in this stage.
  - `prompts/coding/core.md` was subsequently aligned in C.5 to the V1 branch-aware identity/routing contract; no public session ID was added.

### Runtime prompt / npm audit (C.5)

- Status: PASS / dependency remediation deferred
- Notes:
  - `prompts/coding/core.md` now states workspace identity as repository + target ref, permits independent active sessions for different branches, scopes foreground-operation exclusion to one workspace, requires `target_ref` on open, and documents optional target routing/error behavior for exec/status/close.
  - When multiple branches of one repository are active, the runtime prompt explicitly requires subsequent exec/status/close calls to continue carrying the conversation's corresponding `target_ref`.
  - Other Markdown files under `prompts/` were searched; no other repository-only Coding ownership rule required modification.
  - `npm ci --ignore-scripts` reproduced the lockfile without package manifest changes. Installed audit-relevant versions: direct-dev `vite@7.0.0`, direct-dev `vitest@3.2.4`, transitive-dev `@vitest/mocker@3.2.4`.
  - Full `npm audit`: 3 package entries (1 moderate, 1 high, 1 critical). `npm audit --omit=dev`: 0 vulnerabilities. Therefore current findings are limited to development/build/test tooling and are not in the production dependency tree.
  - Vite: npm audit fix target `7.3.6`, non-major.
  - Vitest critical UI-server advisory: upstream patch `3.2.6`; npm audit direct fix target `3.2.7`, non-major.
  - New Vitest / `@vitest/mocker` redirect-mock advisory: upstream patch `4.1.11`; 3.x is not planned to receive the fix. Fully clearing this advisory therefore requires a Vitest 4.x major upgrade. Registry inspection confirms `vitest@3.2.7` still depends on `@vitest/mocker@3.2.7`, so the npm direct-package suggestion is only a partial remediation for the aggregate Vitest findings.
  - No `npm audit fix`, `npm audit fix --force`, package.json edit, or package-lock edit was performed.
  - Audit findings do not block a V1 test build because the production dependency audit is clean and the executable embeds compiled `frontend/dist`; dev-server exposure remains a separate developer-environment risk.

### Executable integration

- Status: ACCEPTED WITH D1 WAIVED (D0 cross-version direction completed; additional V1-primary UI probe not run)
- Notes:
  - D0 `original 2.0.5 primary -> V1 second`: PASS for single-instance conflict isolation. The V1 process exited with code 0 and never became an independent running CWapi instance.
  - D0 observed exactly one CWapi process after the launch attempt and no additional MCP/service, Tunnel client, or listener set; the original listeners remained 32123/32124 and its two Tunnel-client listeners remained unchanged.
  - No `CWapi 已在运行` V1 WarningDialog was observed in this cross-version direction. This is acceptable under the chosen V1 target because the original 2.0.5 primary owns the Wails second-instance callback and does not contain the V1 warning implementation.
  - D1 `V1 primary -> V1 second` additional normal/admin executable testing was explicitly waived by the user for V1 closeout. The expected callback/WarningDialog behavior remains documented from source/unit coverage, but this line is not claimed as a real executable PASS.
  - Runtime regression is verified on the running 2026-09-18 01:25 V1 build: GUI Open Folder visibly opens the workspace, SAFE exposes a 24-hex runtime workspace ID, and an unmodified `go test ./...` completes without the previously observed Windows `Filename too long` failure.
  - Formal V1 acceptance target: guarantee single-instance conflict isolation; provide the explicit WarningDialog when V1 is the already-running primary; do not require the V1 warning when an original 2.0.5 build owns the primary instance.
  - Do not interpret mixed-privilege callback behavior as a V1 IPC regression; it is outside the V1 design scope.

### Upgrade/rollback

- Status: PASS (user-verified V1 upgrade/rollback)
- Notes:
  - The original 2.0.5 -> V1 procedure now explicitly requires exiting CWapi, backing up complete `CWapi-data`, retaining the original 2.0.5 build, and not auto-migrating original V1 64-hex or upstream repository-only workspaces.
  - Rollback is documented as exiting V1, restoring the pre-upgrade data backup when required, and launching the untouched original build.
  - Real V1 upgrade/rollback was user-verified by replacing the executable together with the required files under `prompts/coding/`, while preserving the existing portable data/configuration flow.
