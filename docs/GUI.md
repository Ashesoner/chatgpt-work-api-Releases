# CWapi 2.0.7 GUI — development

窗口固定为 430 × 625、frameless、不可拉伸；主内容区支持鼠标滚轮纵向滚动；标题栏 `×` 只隐藏主窗口到系统托盘，不终止 CWapi，真正退出由托盘菜单执行；Windows Explorer/任务栏重建后，托盘窗口会接收 `TaskbarCreated` 并自动重新注册 CWapi 图标；同权限级别下再次启动正常 CWapi 时，Wails single-instance lock 阻止第二实例正常运行，已有实例恢复/显示窗口并弹出“CWapi 已在运行” Warning MessageDialog。Windows 不同权限/提升级别之间的 callback 属于 Wails/Windows 已知边界，V1 不重做 IPC。主界面通过页签分为 Coding 与 Agent 两页，两个页面只显示并管理各自链路。

## 页面结构

```text
CWapi
Coding | Agent
当前链路的 ChatGPT MCP 账号卡片（含折叠网络设置）
当前链路的运行卡片
当前警告或错误（按严重性分开）
```

- Coding 页：Coding Tunnel、Codex SAFE/FULL、独立的命令网络能力、Advanced 中默认关闭的 Remote Git Rewrite、active session 与 workspaces。
- Agent 页：Agent Tunnel、enabled、Provider URL、API key、bridge Offline/Ready/Busy、pending/claimed/completed；空闲 bridge 因 `AGENT_BRIDGE_STALE` 到期且没有活动请求时显示为“已休眠”，不作为红色错误。
- Tunnel 初始启动失败或异常退出后的自动退避重启显示为“正在重连”；子进程运行后，账号状态继续显示实际观测到的“正在启动 / 已连接 / MCP 未就绪 / 代理不可达 / 控制面不可达 / 连接状态未知”。断开或重配置会取消重启和对应状态监测。
- Tunnel ID 只在可编辑输入框中显示，不再额外渲染重复的“隧道 ID / 复制”行。
- 每个 `CHATGPT MCP 账号`卡片内有一行折叠网络摘要；默认只显示“直连”或 `HTTP · host:port`，点击“设置”才展开直连/HTTP 代理选择和地址输入。前端按与后端一致的 HTTP/HTTPS、host、端口、路径/认证/query/fragment 规则校验；Coding 与 Agent 各自保存网络设置。
- 本地 `127.0.0.1` MCP 地址不在主页面展示；它仍作为本机 MCP 客户端和 Tunnel 的内部转发目标保留。
- 页面不保留日志历史；真正失败使用红色错误栏，非阻塞诊断警告使用单独的提示样式。Coding 警告在可用时带最近一次执行所属 branch；路径、密钥和原始错误详情仍不展示。

Coding 与 Agent 没有模式切换器，两条链可同时工作。页签切换不会清空另一页的输入状态。

所有需要确认的危险操作（断开隧道、删除工作区）使用应用内确认窗口，不调用浏览器原生确认框；窗口明确显示影响范围、取消按钮和带动作含义的确认按钮。

## 配置 mutation

GUI 可以：

- SAFE/FULL 切换；
- Coding 命令网络访问开关；
- Advanced Remote Git Rewrite 开关；
- Agent enable/disable；
- Agent API key regenerate；
- Coding / Agent Tunnel 独立的直连或 HTTP/HTTPS 代理设置；

SAFE/FULL、Coding network access 与 Remote Git Rewrite 都是运行时能力，可在 Coding session 保持打开时切换，不重启 Service，也不改变 active internal Coding session；已在执行的命令保持启动时的配置，下一条 `coding_exec` 使用新设置。Coding/Agent Tunnel 网络设置同样不重启 Service，只替换对应的 `tunnel-client` 子进程；另一条 Tunnel、MCP listener 与 branch-aware Coding session 不受影响。命令网络和 Remote Git Rewrite 默认关闭。开启 Remote Git Rewrite 必须经过应用内确认，文案明确该能力允许 direct force/delete remote updates，但不会解除危险 transport、receive-pack、CWapi 自保护或自动提权边界。其它需要重启 Service 的配置变更在 active Coding session 或 pending/claimed Agent request 存在时仍拒绝。

## Workspace maintenance

现有 overlay 按 workspace 显示 `owner/repository` 与 branch，提供“打开文件夹”和“删除并重建”。前端只持有 repository、target_ref、branch，不保存或拼接本地 workspace path/hash；Open Folder 由后端解析并打开 `<workspace>/repo`。Delete/Rebuild 按 repository + canonical target_ref 精确定位并需要二次确认，继续使用全局 maintenance busy 策略；原 V1 64 位 branch-aware 与上游 repository-only workspace 都只有 metadata 的 repository + target_ref 精确匹配时才可解析，不自动迁移，也不 fallback 到其它 branch。它不是远程 MCP tool。

## 隐私与可用性

- token/key 默认遮蔽；复制失败不得显示成功状态；
- mutation 失败显示当前错误码，并保留尚未成功保存的 Tunnel key 输入；
- UI 不显示完整 prompt、answer、tool arguments 或 Codex transcript；
- 状态来自 Go snapshot，不伪造进度；
- packaged GUI gate 使用真实 `CWapi.exe` 与 WebView 验证启动、布局、mutation guard 和 listener 状态。
