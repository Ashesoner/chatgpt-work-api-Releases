import { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivateAgentTunnelProfile,
  ActivateCodingTunnelProfile,
  AgentCredential,
  AgentTunnelProfiles,
  CodingTunnelProfiles,
  ConnectionInfo,
  DeactivateAgentTunnelProfile,
  DeactivateCodingTunnelProfile,
  DeleteAgentTunnelProfile,
  DeleteCodingTunnelProfile,
  DeleteWorkspace,
  RegenerateAgentAPIKey,
  RuntimeSnapshot,
  SaveAgentTunnelProfile,
  SaveCodingTunnelProfile,
  SetAgentEnabled,
  UpdateCodexAccessProfile,
  UpdateCodexNetworkAccess,
  UpdateCodexRemoteGitRewrite,
} from "../wailsjs/go/main/App";
import { WindowHide } from "../wailsjs/runtime/runtime";

type Page = "coding" | "agent";
type AgentRequestState = { request_id: string; task_id?: string; correlation_id?: string; state?: string; progress?: string; last_activity?: string; hard_deadline_at?: string };
type Snapshot = {
  state: string;
  mcp?: { state?: string; address?: string; error?: string };
  codex_access_profile?: string;
  codex_network_access?: boolean;
  codex_remote_git_rewrite?: boolean;
  codex?: { state?: string; executable?: string; last_error?: string };
  coding?: { state?: string; active?: number; repositories?: string[] };
  workspaces?: { repository_count?: number; repositories?: string[]; invalid_entries?: number };
  agent_enabled?: boolean;
  agent_provider?: { state?: string; address?: string; last_error?: string };
  agent?: { bridge_state?: string; pending?: number; claimed?: number; active?: number; completed?: number; revision?: number; idle_count?: number; last_state?: string; last_error?: string; last_progress?: string; image_bytes?: number; requests?: AgentRequestState[] };
  openai_tunnel?: TunnelSnapshot;
  agent_openai_tunnel?: TunnelSnapshot;
  last_error?: string;
};
type Connections = { coding_mcp?: string; agent_mcp?: string; agent_provider?: string };
type TunnelInfo = { enabled?: boolean; tunnel_id?: string; api_key_present?: boolean; state?: string; last_error?: string };
type TunnelSnapshot = Omit<TunnelInfo, "enabled"> & { configured?: boolean };
type TunnelProfile = { id: string; name: string; tunnel_id: string; api_key_present: boolean; active: boolean; state: string };
type ConfirmState = {
  title: string;
  description: string;
  confirmLabel: string;
  onConfirm: () => Promise<void>;
};

const emptySnapshot: Snapshot = { state: "starting" };

function normalizeTunnel(value?: TunnelSnapshot): TunnelInfo {
  return {
    enabled: Boolean(value?.configured),
    tunnel_id: value?.tunnel_id,
    api_key_present: value?.api_key_present,
    state: value?.state,
    last_error: value?.last_error,
  };
}

function errorCode(value: unknown) {
  const text = value instanceof Error ? value.message : String(value || "");
  return text.match(/[A-Z][A-Z0-9_]{2,}/)?.[0] || (text.trim() ? "OPERATION_FAILED" : "");
}

function snapshotIssue(page: Page, snapshot: Snapshot, tunnel: TunnelInfo) {
  const candidates: unknown[] = [tunnel.last_error];
  if (snapshot.mcp?.state === "failed") candidates.push(snapshot.mcp.error);
  if (snapshot.state === "failed") candidates.push(snapshot.last_error);
  if (page === "coding" && ["failed", "unavailable"].includes(snapshot.codex?.state || "")) candidates.push(snapshot.codex?.last_error);
  if (page === "agent") {
    if (snapshot.agent_provider?.state === "failed") candidates.push(snapshot.agent_provider.last_error);
    if (snapshot.agent_enabled && snapshot.agent?.bridge_state === "OFFLINE") candidates.push(snapshot.agent.last_error);
  }
  return candidates.map(errorCode).find(Boolean) || "";
}

function statusClass(value?: string) {
  const state = (value || "unknown").toLowerCase();
  if (["running", "ready", "active", "completed"].includes(state)) return "ok";
  if (["busy", "claimed", "queued", "starting", "restarting"].includes(state)) return "busy";
  if (["failed", "unavailable", "offline", "blocked", "missing_credentials"].includes(state)) return "bad";
  return "muted";
}

function stateLabel(value?: string) {
  switch ((value || "unknown").toLowerCase()) {
    case "running": return "运行中";
    case "ready": return "就绪";
    case "active": return "活动";
    case "completed": return "已完成";
    case "busy": return "忙碌";
    case "claimed": return "已领取";
    case "queued": return "排队中";
    case "starting": return "启动中";
    case "restarting": return "正在重连";
    case "stopped": return "已停止";
    case "stopping": return "停止中";
    case "failed": return "失败";
    case "unavailable": return "不可用";
    case "offline": return "离线";
    case "blocked": return "已阻止";
    case "missing_credentials": return "缺少凭据";
    case "disabled": return "未启用";
    case "unknown": return "未知";
    default: return value || "未知";
  }
}

export default function App() {
  const [page, setPage] = useState<Page>("coding");
  const [snapshot, setSnapshot] = useState<Snapshot>(emptySnapshot);
  const [connections, setConnections] = useState<Connections>({});
  const [codingTunnel, setCodingTunnel] = useState<TunnelInfo>({});
  const [agentTunnel, setAgentTunnel] = useState<TunnelInfo>({});
  const [codingProfiles, setCodingProfiles] = useState<TunnelProfile[]>([]);
  const [agentProfiles, setAgentProfiles] = useState<TunnelProfile[]>([]);
  const [working, setWorking] = useState(false);
  const [workspaceManagerOpen, setWorkspaceManagerOpen] = useState(false);
  const [confirmState, setConfirmState] = useState<ConfirmState | null>(null);
  const [confirmWorking, setConfirmWorking] = useState(false);
  const [actionError, setActionError] = useState("");
  const [refreshError, setRefreshError] = useState("");
  const refreshRun = useRef<{ token: symbol; promise: Promise<void> } | null>(null);
  const refreshPending = useRef(false);

  const refresh = useCallback(() => {
    const active = refreshRun.current;
    if (active) {
      refreshPending.current = true;
      return active.promise;
    }
    const token = Symbol("refresh");
    const promise = (async () => {
      try {
        do {
          refreshPending.current = false;
          const [nextValue, endpoints, nextCodingProfiles, nextAgentProfiles] = await Promise.all([
            RuntimeSnapshot(),
            ConnectionInfo(),
            CodingTunnelProfiles(),
            AgentTunnelProfiles(),
          ]);
          const next = nextValue as Snapshot;
          setSnapshot(next);
          setConnections(endpoints as Connections);
          const nextCoding = normalizeTunnel(next.openai_tunnel);
          const nextAgent = normalizeTunnel(next.agent_openai_tunnel);
          setCodingTunnel(nextCoding);
          setAgentTunnel(nextAgent);
          setCodingProfiles((nextCodingProfiles || []) as TunnelProfile[]);
          setAgentProfiles((nextAgentProfiles || []) as TunnelProfile[]);
          setRefreshError("");
        } while (refreshPending.current);
      } finally {
        if (refreshRun.current?.token === token) refreshRun.current = null;
      }
    })();
    refreshRun.current = { token, promise };
    return promise;
  }, []);

  useEffect(() => {
    const update = () => void refresh().catch((error) => setRefreshError(errorCode(error)));
    update();
    const timer = window.setInterval(update, 1500);
    return () => window.clearInterval(timer);
  }, [refresh]);

  async function copy(value?: string, label = "已复制") {
    if (!value) return;
    await navigator.clipboard.writeText(value);
  }

  async function action(run: () => Promise<unknown>, success: string) {
    if (working) return false;
    setWorking(true);
    setActionError("");
    try {
      await run();
      await refresh();
      return true;
    } catch (error) {
      setActionError(errorCode(error));
      console.error(success, error);
      return false;
    } finally {
      setWorking(false);
    }
  }

  function askConfirmation(request: ConfirmState) {
    if (working || confirmWorking) return;
    setConfirmState(request);
  }

  async function acceptConfirmation() {
    if (!confirmState || confirmWorking) return;
    const request = confirmState;
    setConfirmWorking(true);
    setConfirmState(null);
    try {
      await request.onConfirm();
    } finally {
      setConfirmWorking(false);
    }
  }

  async function copyAgentKey() {
    try {
      const credential = await AgentCredential() as { api_key?: string };
      await copy(credential.api_key, "Agent API 密钥已复制");
    } catch (error) {
      setActionError(errorCode(error));
      console.error("Agent API 密钥复制失败", error);
    }
  }

  async function saveTunnelProfile(page: Page, profileID: string, name: string, tunnelID: string, apiKey: string) {
    const run = page === "coding"
      ? () => SaveCodingTunnelProfile(profileID, name, tunnelID, apiKey)
      : () => SaveAgentTunnelProfile(profileID, name, tunnelID, apiKey);
    return action(run, `${page === "coding" ? "Coding" : "Agent"} 账号已保存`);
  }

  async function activateTunnelProfile(page: Page, profileID: string) {
    const run = page === "coding"
      ? () => ActivateCodingTunnelProfile(profileID)
      : () => ActivateAgentTunnelProfile(profileID);
    return action(run, `${page === "coding" ? "Coding" : "Agent"} 账号已连接`);
  }

  async function deactivateTunnelProfile(page: Page) {
    const run = page === "coding"
      ? () => DeactivateCodingTunnelProfile()
      : () => DeactivateAgentTunnelProfile();
    return action(run, `${page === "coding" ? "Coding" : "Agent"} 账号已断开`);
  }

  function deleteTunnelProfile(page: Page, profile: TunnelProfile) {
    const label = page === "coding" ? "Coding" : "Agent";
    askConfirmation({
      title: `删除 ${profile.name}？`,
      description: `将从 ${label} 的账号列表中删除该账号及保存的 Runtime API key。${profile.active ? "当前连接也会断开。" : ""}`,
      confirmLabel: "删除账号",
      onConfirm: async () => {
        const run = page === "coding"
          ? () => DeleteCodingTunnelProfile(profile.id)
          : () => DeleteAgentTunnelProfile(profile.id);
        await action(run, `${label} 账号已删除`);
      },
    });
  }
  function deleteWorkspace(repository: string) {
    askConfirmation({
      title: "删除持久化工作区？",
      description: `将删除 ${repository} 的本地工作区。tracked、untracked 和 ignored 文件都会被移除；下一次 coding_open 会重新克隆。`,
      confirmLabel: "删除并重建",
      onConfirm: async () => { await action(() => DeleteWorkspace(repository), `工作区已移除：${repository}`); },
    });
  }

  function changeRemoteGitRewrite(allowed: boolean) {
    if (!allowed) {
      void action(() => UpdateCodexRemoteGitRewrite(false), "远程 Git 重写已禁用");
      return;
    }
    askConfirmation({
      title: "启用远程 Git 重写？",
      description: "将允许 force push 与远程 branch/tag 删除。危险 transport、receive-pack 注入和 CWapi safety refs 仍会被拒绝。",
      confirmLabel: "启用高级能力",
      onConfirm: async () => { await action(() => UpdateCodexRemoteGitRewrite(true), "远程 Git 重写已启用"); },
    });
  }

  const access = snapshot.codex_access_profile || "safe";
  const remoteGitRewrite = Boolean(snapshot.codex_remote_git_rewrite);
  const bridge = snapshot.agent?.bridge_state || "offline";
  const workspaces = snapshot.workspaces?.repositories || [];
  const issue = actionError || refreshError || snapshotIssue(page, snapshot, page === "coding" ? codingTunnel : agentTunnel);
  return (
    <main className="shell">
      <header className="titlebar">
        <div className="brand-row">
          <div className="logo">CW</div>
          <div><strong>CWapi</strong><span>2.0.6</span></div>
        </div>
        <button className="window-button" onClick={WindowHide} aria-label="缩小到托盘" title="缩小到托盘">×</button>
      </header>

      <nav className="page-tabs" aria-label="功能页面" role="tablist">
        <PageTab page="coding" activePage={page} label="Coding" detail="代码与工作区" onSelect={setPage} status={snapshot.coding?.state} />
        <PageTab page="agent" activePage={page} label="Agent" detail="Web GPT 服务" onSelect={setPage} status={snapshot.agent?.bridge_state} />
      </nav>

      <div className="page-content" role="tabpanel" aria-label={`${page} 页面`}>
        {page === "coding" ? (
          <CodingPage
            snapshot={snapshot}
            profiles={codingProfiles}
            working={working}
            workspaces={workspaces}
            access={access}
            networkAccess={Boolean(snapshot.codex_network_access)}
            remoteGitRewrite={remoteGitRewrite}
            onSaveProfile={(id, name, tunnelID, apiKey) => saveTunnelProfile("coding", id, name, tunnelID, apiKey)}
            onActivateProfile={(id) => activateTunnelProfile("coding", id)}
            onDeactivateProfile={() => deactivateTunnelProfile("coding")}
            onDeleteProfile={(profile) => deleteTunnelProfile("coding", profile)}
            onAccessChange={(next) => void action(() => UpdateCodexAccessProfile(next), next === "safe" ? "Codex 已切换为安全模式" : "Codex 已切换为完整模式")}
            onNetworkAccessChange={(allowed) => void action(() => UpdateCodexNetworkAccess(allowed), allowed ? "Coding 网络访问已启用" : "Coding 网络访问已禁用")}
            onRemoteGitRewriteChange={changeRemoteGitRewrite}
            onOpenWorkspaceManager={() => setWorkspaceManagerOpen(true)}
          />
        ) : (
          <AgentPage
            snapshot={snapshot}
            connections={connections}
            profiles={agentProfiles}
            working={working}
            bridge={bridge}
            onCopy={copy}
            onSaveProfile={(id, name, tunnelID, apiKey) => saveTunnelProfile("agent", id, name, tunnelID, apiKey)}
            onActivateProfile={(id) => activateTunnelProfile("agent", id)}
            onDeactivateProfile={() => deactivateTunnelProfile("agent")}
            onDeleteProfile={(profile) => deleteTunnelProfile("agent", profile)}
            onEnabledChange={(enabled) => void action(() => SetAgentEnabled(enabled), enabled ? "Agent 已启用" : "Agent 已停用")}
            onCopyAgentKey={() => void copyAgentKey()}
            onRegenerateAgentKey={() => void action(() => RegenerateAgentAPIKey(), "Agent API 密钥已重新生成")}
          />
        )}
      </div>

      {issue && <div className="current-error" role="alert"><span>当前错误</span><code>{issue}</code></div>}
      <footer>Coding 与 Agent 相互独立，分别在对应页面配置和管理。</footer>

      {workspaceManagerOpen && (
        <WorkspaceManager
          snapshot={snapshot}
          workspaces={workspaces}
          working={working}
          onClose={() => setWorkspaceManagerOpen(false)}
          onDelete={deleteWorkspace}
        />
      )}

      {confirmState && <ConfirmDialog request={confirmState} working={confirmWorking} onCancel={() => setConfirmState(null)} onConfirm={() => void acceptConfirmation()} />}
    </main>
  );
}

function PageTab({ page, activePage, label, detail, status, onSelect }: { page: Page; activePage: Page; label: string; detail: string; status?: string; onSelect: (page: Page) => void }) {
  const active = page === activePage;
  return (
    <button className={`page-tab ${active ? "active" : ""}`} role="tab" aria-selected={active} onClick={() => onSelect(page)}>
      <span className={`service-mark ${page}`}>{page === "coding" ? "C" : "A"}</span>
      <span className="page-tab-copy"><strong>{label}</strong><small>{detail}</small></span>
      <span className={`tab-dot ${statusClass(status)}`} role="img" aria-label={`${label} 状态：${stateLabel(status)}`} title={`${label} 状态：${stateLabel(status)}`} />
    </button>
  );
}

function CodingPage({ snapshot, profiles, working, workspaces, access, networkAccess, remoteGitRewrite, onSaveProfile, onActivateProfile, onDeactivateProfile, onDeleteProfile, onAccessChange, onNetworkAccessChange, onRemoteGitRewriteChange, onOpenWorkspaceManager }: {
  snapshot: Snapshot;
  profiles: TunnelProfile[];
  working: boolean;
  workspaces: string[];
  access: string;
  networkAccess: boolean;
  remoteGitRewrite: boolean;
  onSaveProfile: (id: string, name: string, tunnelID: string, apiKey: string) => Promise<boolean>;
  onActivateProfile: (id: string) => Promise<boolean>;
  onDeactivateProfile: () => Promise<boolean>;
  onDeleteProfile: (profile: TunnelProfile) => void;
  onAccessChange: (value: string) => void;
  onNetworkAccessChange: (allowed: boolean) => void;
  onRemoteGitRewriteChange: (allowed: boolean) => void;
  onOpenWorkspaceManager: () => void;
}) {
  return (
    <>
      <TunnelAccountsCard page="coding" profiles={profiles} working={working} onSave={onSaveProfile} onActivate={onActivateProfile} onDeactivate={onDeactivateProfile} onDelete={onDeleteProfile} />
      <section className="card">
        <div className="card-title"><span>CODEX</span><span className={`pill small ${statusClass(snapshot.codex?.state)}`}>{stateLabel(snapshot.codex?.state)}</span></div>
        <div className="segmented">
          <button disabled={working} className={access === "safe" ? "selected" : ""} onClick={() => onAccessChange("safe")}>安全 SAFE</button>
          <button disabled={working} className={access === "full" ? "selected danger" : ""} onClick={() => onAccessChange("full")}>完整 FULL</button>
        </div>
        <div className="toggle-row">
          <div className="toggle-item"><span>网络访问</span><label className="switch"><input type="checkbox" aria-label="允许 Coding 命令访问网络" checked={networkAccess} disabled={working} onChange={(event) => onNetworkAccessChange(event.target.checked)} /><span /></label></div>
          <div className="toggle-item"><span>远程Git重写</span><label className="switch"><input type="checkbox" aria-label="允许远程 Git 重写" checked={remoteGitRewrite} disabled={working} onChange={(event) => onRemoteGitRewriteChange(event.target.checked)} /><span /></label></div>
        </div>
        <div className="stats-row">
          <StatRow label="活动会话" value={snapshot.coding?.active ?? 0} />
          <StatRow label="工作区仓库" value={snapshot.workspaces?.repository_count ?? 0} />
        </div>
        {(snapshot.coding?.repositories || []).slice(0, 2).map((repo) => <div className="repo" key={repo}>{repo}</div>)}
        <button className="text-button" disabled={working} onClick={onOpenWorkspaceManager}>管理工作区</button>
      </section>
    </>
  );
}

function AgentPage({ snapshot, connections, profiles, working, bridge, onCopy, onSaveProfile, onActivateProfile, onDeactivateProfile, onDeleteProfile, onEnabledChange, onCopyAgentKey, onRegenerateAgentKey }: {
  snapshot: Snapshot;
  connections: Connections;
  profiles: TunnelProfile[];
  working: boolean;
  bridge: string;
  onCopy: (value?: string, label?: string) => void | Promise<void>;
  onSaveProfile: (id: string, name: string, tunnelID: string, apiKey: string) => Promise<boolean>;
  onActivateProfile: (id: string) => Promise<boolean>;
  onDeactivateProfile: () => Promise<boolean>;
  onDeleteProfile: (profile: TunnelProfile) => void;
  onEnabledChange: (enabled: boolean) => void;
  onCopyAgentKey: () => void;
  onRegenerateAgentKey: () => void;
}) {
  return (
    <>
      <TunnelAccountsCard page="agent" profiles={profiles} working={working} onSave={onSaveProfile} onActivate={onActivateProfile} onDeactivate={onDeactivateProfile} onDelete={onDeleteProfile} />
      <section className="card">
        <div className="card-title"><span>AGENT 服务</span><label className="switch"><input type="checkbox" aria-label="启用 Agent 服务" checked={Boolean(snapshot.agent_enabled)} disabled={working} onChange={(event) => onEnabledChange(event.target.checked)} /><span /></label></div>
        <StatRow label="服务提供商" value={stateLabel(snapshot.agent_provider?.state || "disabled")} status={snapshot.agent_provider?.state} />
        <StatRow label="Web GPT" value={stateLabel(bridge)} status={bridge} />
        <div className="triple">
          <Metric label="等待中" value={snapshot.agent?.pending ?? 0} />
          <Metric label="已领取" value={snapshot.agent?.claimed ?? 0} />
          <Metric label="已完成" value={snapshot.agent?.completed ?? 0} />
        </div>
        {(snapshot.agent?.requests || []).length > 0 && <div className="agent-request-list" aria-label="Agent 活动请求">
          {(snapshot.agent?.requests || []).slice(0, 4).map((request) => <div className="agent-request" key={request.request_id}>
            <div className="agent-request-head"><code>{request.task_id || request.request_id}</code><span className={`pill small ${statusClass(request.state)}`}>{stateLabel(request.state)}</span></div>
            {request.progress && <small>{request.progress}</small>}
          </div>)}
        </div>}
        {(snapshot.agent?.image_bytes ?? 0) > 0 && <StatRow label="图片内存" value={`${Math.ceil((snapshot.agent?.image_bytes || 0) / 1024 / 1024)} MiB`} />}
        {snapshot.agent_enabled && <EndpointRow label="服务提供商地址" value={connections.agent_provider || "启动中…"} onCopy={() => onCopy(connections.agent_provider, "服务提供商地址已复制")} />}
        <div className="actions">
          <button disabled={working || !snapshot.agent_enabled} onClick={onCopyAgentKey}>复制 API 密钥</button>
          <button disabled={working || !snapshot.agent_enabled} onClick={onRegenerateAgentKey}>重新生成密钥</button>
        </div>
      </section>
    </>
  );
}

function TunnelAccountsCard({ page, profiles, working, onSave, onActivate, onDeactivate, onDelete }: {
  page: Page;
  profiles: TunnelProfile[];
  working: boolean;
  onSave: (id: string, name: string, tunnelID: string, apiKey: string) => Promise<boolean>;
  onActivate: (id: string) => Promise<boolean>;
  onDeactivate: () => Promise<boolean>;
  onDelete: (profile: TunnelProfile) => void;
}) {
  const label = page === "coding" ? "Coding" : "Agent";
  const [editingID, setEditingID] = useState("");
  const [editorOpen, setEditorOpen] = useState(false);
  const [name, setName] = useState("");
  const [tunnelID, setTunnelID] = useState("");
  const [apiKey, setAPIKey] = useState("");
  const editing = profiles.find((profile) => profile.id === editingID);

  function resetEditor() {
    setEditorOpen(false);
    setEditingID("");
    setName("");
    setTunnelID("");
    setAPIKey("");
  }

  function beginAdd() {
    setEditingID("");
    setName(`账号 ${profiles.length + 1}`);
    setTunnelID("");
    setAPIKey("");
    setEditorOpen(true);
  }

  function beginEdit(profile: TunnelProfile) {
    setEditingID(profile.id);
    setName(profile.name);
    setTunnelID(profile.tunnel_id);
    setAPIKey("");
    setEditorOpen(true);
  }

  async function save() {
    if (await onSave(editingID, name, tunnelID, apiKey)) resetEditor();
  }

  return (
    <section className="card">
      <div className="card-title"><span>CHATGPT MCP 账号</span><span className="pill small ok">{label} 单账号连接</span></div>
      <div className="account-list">
        {profiles.length === 0 && <div className="empty account-empty">暂无已保存账号。</div>}
        {profiles.map((profile) => (
          <div className={`account-item ${profile.active ? "active" : ""}`} key={profile.id} aria-label={`${label} 账号 ${profile.name}`}>
            <div className="account-main">
              <div className="account-name"><strong>{profile.name}</strong><span className={`pill small ${profile.active ? statusClass(profile.state) : "muted"}`}>{profile.active ? stateLabel(profile.state) : "已保存"}</span></div>
              <code title={profile.tunnel_id}>{profile.tunnel_id}</code>
            </div>
            <div className="account-actions">
              {profile.active
                ? <button disabled={working} onClick={() => void onDeactivate()}>断开</button>
                : <button disabled={working || !profile.api_key_present} onClick={() => void onActivate(profile.id)}>连接</button>}
              <button disabled={working} onClick={() => beginEdit(profile)}>编辑</button>
              <button disabled={working} className="danger-text" onClick={() => onDelete(profile)}>删除</button>
            </div>
          </div>
        ))}
      </div>
      {!editorOpen && <button className="text-button account-add" disabled={working} onClick={beginAdd}>+ 添加 {label} 账号</button>}
      {editorOpen && (
        <div className="account-editor">
          <div className="tunnel-panel-title"><strong>{editing ? `编辑 ${editing.name}` : `添加 ${label} 账号`}</strong><button className="editor-close" disabled={working} onClick={resetEditor}>取消</button></div>
          <input className="tunnel-input" aria-label={`${label} 账号名称`} value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：GPT A" autoComplete="off" />
          <input className="tunnel-input" aria-label={`${label} 隧道 ID`} value={tunnelID} onChange={(event) => setTunnelID(event.target.value)} placeholder="tunnel_…" autoComplete="off" />
          <input className="tunnel-input" aria-label={`${label} Runtime API key`} type="password" value={apiKey} onChange={(event) => setAPIKey(event.target.value)} placeholder={editing?.api_key_present ? "留空则保留已保存密钥" : "sk-…"} autoComplete="new-password" />
          <div className="actions"><button disabled={working || !name.trim() || !tunnelID.trim() || (!editing?.api_key_present && !apiKey)} onClick={() => void save()}>保存 {label} 账号</button></div>
        </div>
      )}
    </section>
  );
}
function WorkspaceManager({ snapshot, workspaces, working, onClose, onDelete }: { snapshot: Snapshot; workspaces: string[]; working: boolean; onClose: () => void; onDelete: (repository: string) => void }) {
  return (
    <div className="overlay" role="dialog" aria-modal="true" aria-label="工作区管理">
      <div className="manager">
        <div className="manager-head"><div><p className="eyebrow">CODING 维护</p><h2>持久化工作区</h2></div><button onClick={onClose} aria-label="关闭工作区管理">×</button></div>
        <p className="manager-note">删除工作区会移除本地源码、未跟踪文件和构建缓存。下一次 coding_open 时会重新创建。</p>
        <div className="workspace-list">
          {workspaces.length === 0 && <div className="empty">暂无持久化工作区。</div>}
          {workspaces.map((repo) => <div className="workspace-item" key={repo}><code>{repo}</code><button disabled={working || (snapshot.coding?.active ?? 0) > 0} onClick={() => onDelete(repo)}>删除并重建</button></div>)}
        </div>
        {(snapshot.workspaces?.invalid_entries ?? 0) > 0 && <div className="warning">无效工作区元数据条目：{snapshot.workspaces?.invalid_entries}</div>}
      </div>
    </div>
  );
}

function ConfirmDialog({ request, working, onCancel, onConfirm }: { request: ConfirmState; working: boolean; onCancel: () => void; onConfirm: () => void }) {
  return (
    <div className="overlay confirm-overlay" role="presentation">
      <div className="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="confirm-title" aria-describedby="confirm-description">
        <div className="confirm-icon">!</div>
        <p className="eyebrow">需要确认</p>
        <h2 id="confirm-title">{request.title}</h2>
        <p id="confirm-description" className="confirm-description">{request.description}</p>
        <div className="confirm-actions"><button className="cancel-button" disabled={working} onClick={onCancel}>取消</button><button className="danger-button" disabled={working} onClick={onConfirm}>{working ? "处理中…" : request.confirmLabel}</button></div>
      </div>
    </div>
  );
}

function EndpointRow({ label, value, onCopy }: { label: string; value: string; onCopy: () => void | Promise<void> }) {
  return <div className="endpoint-row"><div><span>{label}</span><code>{value}</code></div><button onClick={() => void onCopy()}>复制</button></div>;
}
function StatRow({ label, value, status }: { label: string; value: string | number; status?: string }) {
  return <div className="stat-row"><span>{label}</span><strong className={status ? statusClass(status) : ""}>{value}</strong></div>;
}
function Metric({ label, value }: { label: string; value: number }) {
  return <div className="metric"><strong>{value}</strong><span>{label}</span></div>;
}
