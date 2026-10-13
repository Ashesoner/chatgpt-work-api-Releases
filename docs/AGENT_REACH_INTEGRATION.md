# Agent Reach × CWapi 集成

本文档记录 CWapi 对 Agent Reach / OpenCLI 的长期集成事实，避免依赖聊天记忆。当前目标是保持 CWapi 通用架构不变，只在 Skill 与 SAFE 运行规则中增加最小适配。

## 架构结论

- `load_skill(name)` 的 MCP 参数结构保持不变。
- 兼容原有 `skills/<id>.md`，并支持目录 Skill：`skills/<id>/SKILL.md` + `references/*.md`。
- reference 不进入启动 Skill 清单；父 Skill 加载后声明隐藏 alias，例如 `agent-reach.social`，仍通过 `load_skill(name)` 获取。
- 可选 `CWAPI.md` 作为 CWapi 专用 overlay，加载时追加到父 Skill；上游 `SKILL.md` / `references/*.md` 保持原样。
- ChatGPT 原生 Web/GitHub 继续负责普通公开网页、新闻、官方文档和公开 GitHub；Agent Reach 主要负责平台专属结构数据、浏览器登录态、字幕/音频以及用户明确要求使用 Agent Reach 的场景。
- Coding 场景直接使用 Coding surface 的 `load_skill` 后继续 `coding_exec`，不需要绕到 CWapi_Web。
- 目前没有证据需要新增通用 SAFE External Tool Profile，也不扩大 SAFE 到整个真实用户目录。

## 目录 Skill 安全边界

loader 当前强制：

- 单个 Markdown 最大 1 MiB；
- 单个目录 Skill 最多 64 个 `references/*.md`；超过时目录 Skill 整体不加载；
- symlink、Windows junction 等 linked path 不允许；解析后的路径必须留在 Skill root 内；
- legacy 单文件 Skill 优先保留既有 ID；目录 Skill 主 ID 或 reference alias 冲突时不会覆盖 legacy Skill，并产生 warning；
- hidden reference alias 不进入 `SkillList()`；只有 hidden alias 时清单仍显示 `(none)`；
- `CWAPI.md` 存在但无效时目录 Skill 整体不加载，避免只应用一半规则；
- YAML frontmatter 只解析当前需要的 `name` / `description` 子集，不承诺通用 YAML 兼容。

相关回归测试位于 `internal/v2/promptstore/directory_skills*_test.go` 与 `internal/v2/mcpserver/skill_tools_test.go`。

## Agent Reach 上游同步

当前上游：

- Repository: `https://github.com/Panniantong/Agent-Reach`
- Package version: `1.5.0`
- 当前 bundled 文件最初同步自 upstream main commit `94f06c1969dfc1834001269d79d3ad0972d9dee6`

`scripts/sync-agent-reach-skill.ps1` 从本机已安装的 Agent Reach package 同步 `SKILL.md` 和 `references/*.md`；`CWAPI.md` 保留不动；`UPSTREAM.json` 记录 package version、操作者提供的 upstream commit 以及每个上游文件的 SHA-256。测试会校验 bundled 文件与 manifest hash 一致。

这些上游 Markdown 按原字节同步；`.gitattributes` 对 `SKILL.md` 与 `references/*.md` 关闭 Git whitespace 报警，避免为了通过 `git diff --check` 而改写第三方原文。CWapi 自有的 `CWAPI.md` 仍使用正常 whitespace 检查。

同步后必须重新 Review 上游差异并运行 promptstore 测试；不能仅凭版本号假定兼容。

## Windows SAFE 与外部工具

已验证：

- SAFE 可以运行 `C:\Users\<user>\.agent-reach-venv` 中的 Agent Reach / Python；
- Agent Reach venv Python 可在 SAFE 下正常 HTTPS 访问；
- SAFE 持久 cache 可保存隔离配置；
- 用户目录里坏掉的 `Python312\Scripts\yt-dlp.exe` 不应优先使用；`.agent-reach-venv\Scripts\yt-dlp.exe` 版本 `2026.08.19` 已在 SAFE 下真实完成 YouTube 搜索和元数据读取；
- 在隔离 `XDG_CONFIG_HOME` 为 yt-dlp 设置 `--js-runtimes node` 后，`agent-reach doctor --json` 的 YouTube 为 `ok`；
- Windows `curl.exe` 在受限 token 下可能遇到 Schannel 凭据错误；这不是切换 FULL 的理由，优先使用 Agent Reach venv Python 路径；
- Unicode 输出使用 Python `-X utf8`，避免 Windows 控制台编码导致 JSON/文本损坏。

`prompts/skills/agent-reach/CWAPI.md` 保存当前 SAFE bootstrap；它只临时调整子进程 PATH 和隔离 HOME，不修改系统 PATH。

## OpenCLI 安装与 daemon 边界

当前固定验证版本：

- OpenCLI CLI `1.8.8`；
- Browser Bridge `1.0.24`；
- Node.js 本机已满足 OpenCLI `>=20.18.1` 要求。

OpenCLI 安装在真实用户目录独立 prefix：

```text
C:\Users\<user>\.opencli-cli\node_modules\.bin\opencli.cmd
```

CWapi 只在子进程 PATH 中临时加入该目录。OpenCLI daemon 必须由普通 Windows 用户会话启动并长期运行在 `127.0.0.1:19825`；SAFE 只连接已有 daemon，不运行 `opencli doctor`、`opencli daemon restart` 或其它会自动创建/替换 daemon 的命令。原因是 CWapi SAFE 会回收命令进程树，而 OpenCLI 需要持久后台 daemon。

Browser Bridge 复用用户自己已登录并明确控制的 Chrome 会话。SAFE 不为此开放真实用户 HOME，也不自行读取浏览器 Cookie。

## 操作权限

OpenCLI 默认按只读能力使用：搜索、正文、元数据、字幕、评论、楼中楼和页面提取可以在用户请求信息时执行。

发布、评论、点赞、收藏、关注/取关、提交表单、删除、修改账号等有外部副作用的操作，必须取得用户对该具体动作的明确授权。

付费内容只使用用户账号本身已有权益；不绕过充电、订阅、大会员、付费墙或 DRM。工具的 `--force` 只能跳过工具自身的保守预检查，不能作为绕过平台授权的手段。

## 已验证能力

| 能力 | 状态 | 说明 |
|---|---|---|
| `load_skill("agent-reach")` | PASS | 主 Skill 可加载，CWAPI overlay 与 reference alias 生效 |
| reference alias | PASS | `web/social/video/dev/...` 可按需加载且不进入顶层清单 |
| Web / Jina Reader | PASS | SAFE 下实际读取网页正文 |
| B站公开搜索/热门 | PASS | 返回真实视频元数据 |
| B站登录态 | PASS | SAFE → OpenCLI → Browser Bridge 已读取当前登录账号信息 |
| B站视频元数据 | PASS | 标题、作者、互动、付费标记等可读 |
| B站字幕 | PASS | 实际返回逐句时间轴字幕 |
| B站评论 | PASS | 一级评论、点赞、回复数、时间可读 |
| B站楼中楼 | PASS | 通过父评论 `rpid` 实际读取回复 |
| B站充电问答 | PASS 到权限边界 | 已登录页面可读；账号无对应档位权益时仍正确显示付费门槛，不绕过 |
| B站充电专属视频 | NOT REQUIRED | 用户决定不再作为集成验收项 |
| 小红书登录态 | PASS | Browser Bridge 已复用当前登录会话 |
| 小红书 Feed | PASS | 推荐笔记可读 |
| 小红书笔记正文 | PASS | 正文、点赞、收藏、评论数、标签可读 |
| 小红书评论/楼中楼 | PASS | 作者、用户 ID、正文、点赞、时间、reply_to、评论图片均已实际返回 |
| 小红书关键词搜索 | UPSTREAM BUG | 登录已确认；OpenCLI 1.8.8 当前可触发 `ambiguous_option`，见下方已知问题 |
| YouTube yt-dlp | PASS | 搜索、元数据、字幕列表已验证；评论能力属于 best-effort，未保证完整 |
| V2EX | PASS | 公开主题/回复 API 已验证 |
| RSS | PASS | feedparser 已实际解析 |
| Reddit / Twitter / Facebook / Instagram | PENDING | 仅在需要时验证对应登录态；不作为本轮 CWapi 收口阻塞项 |
| `bili-cli` | DEFERRED | OpenCLI 已覆盖当前 B站主要需求，暂不增加依赖 |

## 已知 OpenCLI 上游问题

### `Navigation rejected.`

OpenCLI issue #2487 截至 2026-10-13 仍为 open。Browser Bridge 1.0.24 在部分 Chromium 版本上会在导航前 detach debugger，随后 `chrome.tabs.update` 被浏览器拒绝。当前临时策略仅用于只读操作：遇到精确的 `Navigation rejected.` 时最多重试一次 `--trace retain-on-failure`；不能把它误判成 SAFE、网络或账号权限问题，也不能无限重试。

### 小红书搜索 `ambiguous_option`

本机已登录状态下真实复现。OpenCLI PR #2460 说明当前搜索页会把筛选 chip 以相同位置重复渲染，1.8.8 的严格匹配会判定为歧义；issue #2562 还记录了新搜索布局和风控变化。当前不修改本机 OpenCLI 安装文件；等待上游合并/发布后再复测。Feed、笔记、评论和楼中楼不受此问题阻塞。

## 评论能力边界

- B站：OpenCLI `comments` 支持一级评论，并可按父 `rpid` 读取楼中楼；本机已实测。
- 小红书：OpenCLI `comments` 支持一级评论与嵌套回复；本机已实测，含评论图片字段。
- YouTube：yt-dlp 可抓取部分评论，但属于网页抓取的 best-effort，可能缺失。
- Reddit：OpenCLI 设计上可读取帖子全文与评论树，需要登录态后再验证。
- V2EX：公开 API 可读取主题回复，已验证。
- Twitter/X：对应后端可读取推文/回复，但当前账号登录态未做本轮验收。
- Facebook / Instagram：当前 Agent Reach 路线不承诺任意帖子的完整评论树，因此不能标记为通用评论抓取已支持。

## 暂缓事项

本轮不实现：

- 通用 SAFE External Tool Profile；
- 向整个真实 `C:\Users\<user>` 开放 SAFE；
- 新的 Agent Reach MCP tool schema；
- 默认安装 `bili-cli`；
- 将 FULL 作为 Agent Reach 常规运行模式；
- 自行 patch OpenCLI 的小红书搜索或 Browser Bridge 导航 bug。

## 提交前检查

至少执行：

```text
go test ./internal/v2/promptstore -count=1
go test ./internal/v2/mcpserver -count=1
go test ./...
go vet ./...
git diff --check
frontend: npm test -- --run
```

构建如重新生成 `frontend/wailsjs` 且与本功能无关，不纳入本次提交。最终 EXE 还需检查 FileVersion/ProductVersion、SHA-256 与 Skill 资源可发现性。commit / push 需要单独授权。
