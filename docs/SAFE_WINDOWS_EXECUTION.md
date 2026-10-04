# Windows SAFE command startup and workspace lifecycle

SAFE uses the pinned Codex 0.150.1 `workspaceWrite` backend with the Windows
`unelevated` restricted token. FULL uses `dangerFullAccess`. Network access and
remote Git rewrite remain independent capabilities. This fix does not switch
SAFE to FULL or broaden its writable roots.

## Capability identity lifetime

The Codex backend persists capability SIDs in `CODEX_HOME/cap_sid` and installs
inheritable Windows ACL entries for writable roots before starting a target
process. Previously CWapi created a fresh home and random identities for every
command, then deleted the home. The inherited ACL entries survived, so repeated
SAFE execution accumulated identities on the repository and reusable caches.
Updating a root ACL can cause Windows to propagate inheritance across its tree.
Large trees and accumulated ACLs therefore amplify startup cost even for echo.

This is a CWapi integration defect: per-command disposal of CODEX_HOME conflicts
with the backend's persistent capability ledger. Related reports in the official
Codex repository describe [capability/ACL accumulation (#41169)](https://github.com/openai/codex/issues/41169),
[large-root pre-spawn setup cost and fresh-home retriggering (#34889)](https://github.com/openai/codex/issues/34889),
[persistent host ACL/ownership changes (#48721)](https://github.com/openai/codex/issues/48721),
and [DACL capacity/startup failure (#50349)](https://github.com/openai/codex/issues/50349).
These are user reports, with different versions/backends; elevated ownership,
MXC comparisons and deny-ACE amplification are not established for CWapi's
pinned unelevated backend. Stable identity reuse addresses CWapi's repeated
provisioning defect, not every upstream Windows sandbox issue.

CWapi now retains only a random per-workspace identity seed at
`CWapi-data/runtime/workspaces/<workspace-key>/sandbox-identity.json`. Every
command still has an independent temporary CODEX_HOME, config, and environment.
The pinned backend's `cap_sid` file is seeded with stable, root-specific
capabilities for the current request. Another workspace has a different seed;
roots omitted from a request do not appear in its capability maps. Concurrent
commands never share a mutable Codex home. Corrupt or mismatched identity state
fails explicitly rather than silently minting a new identity.

The `cap_sid` adapter is coupled to the executable hash and source commit in
`config/codex-runtime.lock.json`. Runtime upgrades must rerun the opt-in native
ACL stability and cross-workspace write-denial tests. Removing the identity
seed while retaining an existing repo/cache creates another identity and can
reintroduce ACL growth; do not routinely clear it as a command cache.

Ordinary `coding_close`, resume and Host recreation retain the seed. Explicit
GUI Delete/Rebuild removes the selected branch's repository and runtime tree,
including its seed, under the existing maintenance guard. A later rebuild is a
new workspace generation and may obtain a new seed; it does not reclaim old
capability ACEs from shared/external roots. Preserve local work before deletion.
This patch does not change Delete/Rebuild behavior or add automatic ACL repair.

## Readiness and diagnostics

`coding_open` reserves a Git workspace and verifies runtime integrity. It does
not execute a capability probe. `coding_status.state=ready` means no operation
owns the foreground slot; it is not proof that the next command can execute.

`coding_status.last_execution` reports `unverified` before an execution request,
then the last `run`/`start` request's state, time, bounded error, and diagnostics.
It is session-local and branch-specific, not a durable health certificate.
Persistent-process status is still queried separately. The GUI displays the
latest active session's request error and its observed phase. A normal nonzero
command exit does not prove a broken sandbox.

Diagnostics contain timings for runtime preparation, resolution, safety checks,
environment preparation, integrity checking, identity setup, app-server startup,
sandbox readiness/setup, command RPC, and cleanup. They do not include command
arguments, environment values, identity seeds, or command output.

`command_exec` spans **both sandbox startup and target execution/completion**.
The pinned Windows command RPC does not expose a reliable target-start event.
An error marked `phase=command_exec target_start=unknown` must not be described
as a proven sandbox-startup timeout without separate evidence such as a target
startup marker. Caller cancellation is distinguished from deadline timeout;
the same deadline error is normalized consistently across the response race.

A cheap root ACL observation adds `workspace_acl_entries` and the advisory
`WINDOWS_WORKSPACE_ACL_LARGE` at 256 or more entries. This heuristic neither
attributes rules to CWapi nor blocks execution. Legitimate enterprise ACLs can
also trigger it. No expensive recursive file-count scan runs on every command.

## Large trees and existing ACL accumulation

Ignored files are still inside the physical writable root: `.gitignore` does
not exclude them from Windows ACL inheritance. Dependency trees, build outputs,
venvs and SDKs are ordinary workspace contents, not inherently invalid usage.
The identity-lifetime defect should be fixed, not documented away as a ban on
large projects. Initial ACL provisioning can still cost more on large trees.
Measurements on one machine are not a universal file-count or size limit.

SDKs and large caches can be kept outside a repository when convenient, but
SAFE cannot write arbitrary external paths. Its isolated HOME/LOCALAPPDATA and
managed cache environment also differ from FULL. Moving tools to the current
user's LOCALAPPDATA alone does not establish that a SAFE build can find or write
them. Validate actual tool paths and cache destinations under the chosen profile.

The fix prevents repeated commands with the same workspace/canonical writable
roots from minting additional identities; newly authorized roots/workspaces
still receive distinct identities as required for isolation. It does **not**
remove historical ACLs.
CWapi cannot infer ownership of arbitrary SID rules after the old homes were
deleted. Never bulk-delete unknown SIDs or run a blanket ACL reset as repair.
For a heavily affected workspace, preserve all uncommitted/ignored artifacts
and local commits, close its processes, and prepare a fresh workspace under
reviewed backup/migration steps. Retain the old directory until verification.
Existing workspaces are not automatically moved, deleted or recloned.

## Close and shutdown

The owned Windows job is explicitly terminated and checked for zero active
processes before closing its handle. Foreground timeout/cancellation waits for
command cleanup, and persistent stop waits for its watcher. These operations
target owned jobs, never a global process-name kill. Cleanup errors propagate
instead of being discarded. Persistent launch respects caller cancellation
until ownership is transferred, then survives the initiating HTTP request.

Windows can delay termination during kernel filesystem work. Cleanup waits are
bounded; `PROCESS_JOB_TERMINATION_TIMEOUT`, `CODEX_COMMAND_PROCESS_EXIT_TIMEOUT`
or `CODEX_COMMAND_CLEANUP_TIMEOUT` means release is not confirmed. Do not rename
or delete based solely on the foreground slot becoming ready after such an
error. Unrelated IDE, Java/Gradle, Explorer, antivirus or other handles can also
lock a workspace. Closing the window to the tray is not application exit.

## Native regression checks

Use a short, existing temporary fixture parent. These tests create and remove
only their own generated subdirectories. The runtime executable must match
the pinned hash. Running inside another restricted Windows token can fail with
`CreateRestrictedToken failed: 87`; that is not a valid native baseline.

```powershell
$env:CWAPI_CODEX_TEST_EXECUTABLE = 'C:\path\to\pinned\codex.exe'
$env:CWAPI_TEST_ROOT = 'C:\short\test-fixtures'
$env:CWAPI_TEST_COUNTS = '0,10000,20000,50000'
$env:CWAPI_TEST_EXPECT_SUCCESS = '1'
$env:CWAPI_TEST_REOPEN = '1'
go test ./internal/codex -run TestWindowsRemovedWritableRootCapabilityDenied -v -count=1 -timeout 2m
go test ./internal/v2/codextoolhost -run 'TestWindowsWorkspaceMatrix|TestWindowsCapabilityIsolation|TestWindowsForegroundTimeoutReleasesWorkspace' -v -count=1 -timeout 8m
go test ./internal/v2/coding -run TestWindowsCodingCloseAndShutdownReleaseTrees -v -count=1 -timeout 3m
```

Optional fixture controls: `CWAPI_TEST_LAYOUT=deep|wide`,
`CWAPI_TEST_FILE_BYTES` (up to 32 MiB per file), and
`CWAPI_TEST_SEED_FROM` (reads a source DACL and copies it **only to the generated
fixture**). Clear seed/layout/size controls between independent cases. Ordinary
`go test ./...` skips native runtime checks without opt-in variables.
