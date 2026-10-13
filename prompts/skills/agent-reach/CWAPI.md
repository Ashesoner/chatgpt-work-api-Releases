# CWapi integration override

This file is a CWapi-only overlay. The upstream `SKILL.md` and `references/*.md` stay byte-for-byte unchanged so they can be re-synced without losing local integration rules.

## Routing inside ChatGPT Web

The upstream rule that says Agent Reach must handle every internet lookup is narrowed for CWapi:

- Use ChatGPT's native Web/GitHub tools for ordinary public webpages, current news, official documentation, and public GitHub material when those native tools already fit the request.
- Use Agent Reach when the task needs platform-specific structured data, an existing logged-in browser session, Bilibili/Xiaohongshu/Reddit/Twitter-specific behavior, video subtitles/audio, or when the user explicitly asks to use Agent Reach.
- In a CWapi Coding workflow, load this Skill with the Coding surface's own `load_skill(name)` and continue with `coding_exec`; do not call the separate CWapi_Web surface merely to load the same Skill.

## Windows SAFE bootstrap

Resolve the real Windows user profile before changing isolated environment variables:

```powershell
$hostProfile = [Environment]::GetFolderPath([Environment+SpecialFolder]::UserProfile)
$venvRoot = Join-Path $hostProfile '.agent-reach-venv'
$venvScripts = Join-Path $venvRoot 'Scripts'
$ar = Join-Path $venvScripts 'agent-reach.exe'
$py = Join-Path $venvScripts 'python.exe'
$opencliRoot = Join-Path $hostProfile '.opencli-cli'
$opencliBin = Join-Path $opencliRoot 'node_modules\.bin'

if (-not (Test-Path $ar) -or -not (Test-Path $py)) {
    throw 'Agent Reach venv is missing'
}

# Prefer known-good per-user tool installations without changing the system PATH.
# Agent Reach's venv wins over the broken user-level yt-dlp shim. If the isolated
# OpenCLI install exists, expose only its command directory to this child process.
$pathParts = @($venvScripts)
if (Test-Path $opencliBin) { $pathParts += $opencliBin }
$env:PATH = ($pathParts -join [IO.Path]::PathSeparator) + [IO.Path]::PathSeparator + $env:PATH
```

For the current SAFE validation phase, keep Agent Reach's own persistent state in the workspace-scoped SAFE cache rather than opening the entire real user profile:

```powershell
$toolHome = Join-Path $env:XDG_CACHE_HOME 'agent-reach-home'
$env:HOME = $toolHome
$env:USERPROFILE = $toolHome
$env:XDG_CONFIG_HOME = Join-Path $toolHome '.config'
$env:MCPORTER_CONFIG = Join-Path $toolHome 'mcporter.json'
New-Item -ItemType Directory -Force -Path $env:XDG_CONFIG_HOME | Out-Null

# The isolated profile has its own yt-dlp config. Point yt-dlp at the available
# Node.js runtime so Agent Reach doctor and real execution agree.
$ytCfg = Join-Path $env:XDG_CONFIG_HOME 'yt-dlp\config'
New-Item -ItemType Directory -Force -Path (Split-Path $ytCfg) | Out-Null
if (-not (Test-Path $ytCfg) -or -not (Select-String -Path $ytCfg -Pattern '--js-runtimes' -Quiet)) {
    Add-Content -Path $ytCfg -Value '--js-runtimes node'
}

& $ar doctor --json
```

This isolation recipe is for Agent Reach and zero-config CLI validation. Browser-backed OpenCLI sessions are a separate validation step; do not widen SAFE to the entire real user profile just to make a browser channel work.

## OpenCLI daemon boundary

CWapi SAFE may run the already-installed OpenCLI CLI, but it must not own the OpenCLI daemon lifecycle. OpenCLI starts its browser daemon as a detached child process; CWapi SAFE intentionally reclaims command process trees, so a daemon started from `coding_exec` is not a valid persistent setup.

During the current integration phase:

- Install OpenCLI and start/restart its daemon from the normal Windows user session, outside CWapi SAFE.
- Keep the Chrome Browser Bridge extension enabled in the user's normal browser profile.
- Before any SAFE OpenCLI adapter/browser command, require an already-running daemon on `127.0.0.1:19825` with a connected extension. Agent Reach's `doctor` performs a read-only loopback status probe and is preferred for this preflight.
- Do not run `opencli doctor`, `opencli daemon restart`, or another command that would auto-start/replace the daemon from SAFE. If the daemon is missing/stale/disconnected, stop and ask the user to repair it in the normal Windows session.
- Keep SAFE `HOME`/`USERPROFILE` isolated. Do not expose the entire real user profile merely to reuse browser login state; Browser Bridge carries the logged-in Chrome session over localhost.
- If multiple Chrome profiles are connected, pass an explicit OpenCLI profile/context rather than opening the real `.opencli` configuration directory.

OpenCLI is treated as read-only by default in CWapi. Search, read, metadata, subtitle, comments, and page extraction may run when the user asked for the information. Mutating browser actions such as publish, comment, like, follow/unfollow, form submission, deletion, or account changes require explicit user authorization for that action.

## Known Windows SAFE behavior

- The venv `yt-dlp.exe` is the preferred YouTube backend under SAFE. Do not use the broken user-level shim if both are present.
- If Windows `curl.exe` fails under the restricted token with Schannel `SEC_E_NO_CREDENTIALS`, do not switch to FULL. Use the Agent Reach venv Python/Agent Reach channel implementation for the same read-only request.
- Use `python.exe -X utf8` when emitting structured Unicode text through the Windows console.
- A `doctor` result is a capability snapshot, not proof that a specific logged-in page, subtitle, or paid-content entitlement works. Validate the actual target read.

## Login and paid-content boundary

Use only content that the user's own logged-in account is entitled to view. Never bypass payment, subscription, DRM, or platform access controls. Browser-backed tests must stop if the account lacks the required entitlement.

## OpenCLI Chromium navigation workaround

Current validated combination: OpenCLI CLI 1.8.8 + Browser Bridge extension 1.0.24 on a Chromium-family browser. Upstream issue #2487 is still open: browser-backed adapters can fail with `Navigation rejected.` when the extension detaches the Chrome debugger immediately before `chrome.tabs.update` navigation.

- Do not classify this error as a CWapi SAFE, network, or account-auth failure by itself.
- Retry the same read-only adapter command once with `--trace retain-on-failure`. Trace mode keeps network capture active and avoids the upstream detach/navigation race. Do not loop repeated retries.
- If the trace retry succeeds, continue the read-only task and record the upstream workaround. If it still fails, investigate the actual adapter/auth/site error.
- This workaround is temporary; re-check upstream issue #2487 when OpenCLI or the Browser Bridge extension is upgraded.

On Windows, URL arguments containing command-shell metacharacters such as `&` can be reparsed when invoking the npm-generated `opencli.cmd` wrapper from PowerShell. For exact arbitrary URLs, bypass the `.cmd` wrapper and invoke OpenCLI's package entry point directly with Node so the URL remains one argument:

```powershell
$opencliMain = Join-Path $opencliRoot 'node_modules\@jackwener\opencli\dist\src\main.js'
& node $opencliMain browser <session> open $url
```