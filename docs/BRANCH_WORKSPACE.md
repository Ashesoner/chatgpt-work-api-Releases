# Branch-aware Workspace V1

> Baseline: upstream CWapi 2.0.5. This document records the design, user workflow, upgrade notes, compatibility boundaries, and custom changes for this fork.

## 1. Goal

CWapi 2.0.5 currently treats one Git repository as one durable workspace. This prevents multiple ChatGPT conversations from working on different branches of the same repository at the same time.

V1 changes workspace identity from repository-only to repository + canonical target ref, while preserving existing behavior for the same repository + same branch.

Target behavior:

- same repository + different branches: allowed concurrently;
- same repository + same branch: keep existing busy/resume behavior;
- different repositories: keep existing behavior;
- no change to Agent, Tunnel, coding_exec, SAFE/FULL, Network Access, Remote Git Rewrite, Skills, Git safety policy, or Codex ToolHost.

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

## 3. Coding concurrency rules

- `repo + branch-a` and `repo + branch-b` may be active at the same time.
- a second independent open of `repo + branch-a` must continue to use the original BUSY/resume semantics.
- close/cleanup/resume must use the same branch-aware identity as open.

All uses of Coding `active`, `opening`, and equivalent repository-key lookups must be reviewed together to avoid stale BUSY entries or deleting another branch session's ownership.

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

The existing repository summary/index fields should remain available for compatibility. A new structured workspace list should be added rather than replacing existing repository-only fields.

V1 intentionally keeps the existing conservative maintenance lock: destructive workspace maintenance remains blocked while Coding activity is present. Branch-scoped maintenance locking can be considered later.

## 5. Old workspace compatibility

V1 does not automatically rename or migrate old repository-only workspace directories.

Reason: an old workspace may contain uncommitted work, local commits, runtime state, or metadata. Automatic migration adds risk to an otherwise small change.

Upgrade behavior:

1. back up the existing `CWapi-data` directory before switching versions;
2. keep the old CWapi installation available for rollback;
3. start the modified build only after the original CWapi process has exited;
4. first open of a branch under V1 creates/uses the branch-aware workspace format;
5. old workspace directories are left untouched until the user intentionally cleans them up.

## 6. Original CWapi and modified CWapi conflict prevention

The modified build must not run alongside another CWapi instance.

At startup, reuse CWapi/Wails existing single-instance mechanism where possible. If another instance is already running, show a clear dialog explaining that concurrent CWapi instances may conflict through ports, Tunnel state, runtime state, and workspace data, then exit the newly launched instance.

Do not rely only on process-name enumeration unless the existing single-instance mechanism cannot provide the required behavior.

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

Opening another independent session for the same repository + same branch should still be blocked or resumed according to existing CWapi semantics.

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

## 11. Low-priority follow-up: Windows path length

Workspace directories currently use long hash names similar to:

```text
6f348d1271dee80644fb004738e81958af340a0991708d4612f3c0a98279d88f
```

Combined with deep repository paths, this may eventually cause Windows/tool path-length problems for some commands.

This is explicitly **not part of V1**. It should be revisited only after V1 is complete, or earlier if testing proves that path length causes a real failure. Any future change must preserve stable workspace identity and backward compatibility.

## 12. Custom changes relative to upstream 2.0.5

Planned V1 changes in this fork:

1. branch-aware durable workspace identity;
2. concurrent Coding sessions for different branches of the same repository;
3. preserve same-branch BUSY/resume behavior;
4. branch-aware workspace index details;
5. branch display in workspace management GUI;
6. Open Folder action for a workspace;
7. branch-scoped Delete and Rebuild target;
8. startup conflict notice when another CWapi instance is already running;
9. tests and upgrade/use documentation.
