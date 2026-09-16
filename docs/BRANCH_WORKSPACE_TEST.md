# Branch-aware Workspace V1 Test Plan

This file records the test plan, execution steps, expected results, and final acceptance results for the branch-aware workspace change.

## 1. Test principle

Development and most tests are performed through the currently working original CWapi against the fork's cloned source workspace. The original running CWapi executable is not replaced during source-level testing.

The modified executable is launched only in the final integration stage. Before that stage, the original CWapi is exited.

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

1. open `repo/main`;
2. open `repo/branch-a` concurrently;
3. both sessions can execute commands;
4. both sessions point to different repo paths;
5. each session reports the correct current branch;
6. a second independent open of `repo/main` keeps existing BUSY/resume semantics;
7. closing `repo/branch-a` does not affect `repo/main`;
8. session cleanup removes only the matching branch-aware `active`/`opening` entry;
9. closing all sessions leaves no stale BUSY state.

Implementation review requirement:

Search all uses of:

```text
active[
opening[
repositoryKey
```

and verify they use the same shared branch-aware identity rule where workspace ownership is intended.

## 4. Stage C — Build and GUI compile tests

Original CWapi may remain running.

Required checks:

1. Go build succeeds;
2. frontend build succeeds;
3. generated/bound frontend types compile;
4. old repository summary/index fields remain compatible;
5. new structured workspace list compiles and renders;
6. no unrelated screen or service requires code changes.

Do not launch the modified CWapi executable yet.

## 5. Stage D — Real executable integration tests

Before starting:

```text
[ ] Preserve/commit source changes.
[ ] Close active development Coding sessions as appropriate.
[ ] Exit the original CWapi completely.
[ ] Confirm the modified build is the only CWapi instance to be launched.
```

### D1. Single-instance conflict protection

Test both orders when practical:

1. original CWapi running -> attempt modified build;
2. modified build running -> attempt another CWapi instance.

Expected:

- second instance does not continue running;
- user receives a clear conflict message;
- no parallel access to ports, Tunnel state, runtime data, or workspace data occurs.

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
8. confirm old repository-only workspace directories are not automatically renamed or deleted;
9. verify rollback by exiting modified build and reopening original CWapi with preserved original data if needed.

## 7. Path-length follow-up

Workspace hash directory names are currently long. V1 does not change them.

During all stages, record any command failure that appears related to full path length. If such a failure occurs, stop and document:

- full failing path;
- command/tool that failed;
- Windows error/message;
- whether long-path support is enabled;
- whether failure disappears with a shorter temporary root.

Only then consider a separate post-V1 workspace-path shortening design.

## 8. Acceptance criteria

V1 is accepted only if all are true:

```text
[ ] Same repository + different branches can work concurrently.
[ ] Same repository + same branch keeps existing BUSY/resume behavior.
[ ] Workspace paths are branch-specific.
[ ] Close/cleanup does not leave stale BUSY entries.
[ ] GUI lists repository + branch separately.
[ ] Open Folder targets the correct workspace.
[ ] Delete and Rebuild affects only the selected branch workspace.
[ ] Original major Coding/Agent/Tunnel/security controls regressions are absent.
[ ] Second CWapi instance is blocked with a clear user-facing message.
[ ] Upgrade and rollback steps are documented and verified.
```

## 9. Test results

Fill this section during implementation.

### Source/unit

- Status: NOT RUN
- Notes:

### Coding concurrency

- Status: NOT RUN
- Notes:

### Build/GUI

- Status: NOT RUN
- Notes:

### Executable integration

- Status: NOT RUN
- Notes:

### Upgrade/rollback

- Status: NOT RUN
- Notes:
