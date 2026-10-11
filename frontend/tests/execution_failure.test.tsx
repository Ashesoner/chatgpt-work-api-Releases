// @vitest-environment jsdom
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import App from "../src/App";

const fixture = vi.hoisted(() => ({ snapshot: {} as Record<string, unknown> }));
vi.mock("../wailsjs/go/main/App", () => ({
  RuntimeSnapshot: vi.fn(async () => fixture.snapshot), ConnectionInfo: vi.fn(async () => ({})),
  CodingTunnelProfiles: vi.fn(async () => []), AgentTunnelProfiles: vi.fn(async () => []),
  ActivateAgentTunnelProfile: vi.fn(), ActivateCodingTunnelProfile: vi.fn(), AgentCredential: vi.fn(),
  DeactivateAgentTunnelProfile: vi.fn(), DeactivateCodingTunnelProfile: vi.fn(), DeleteAgentTunnelProfile: vi.fn(),
  DeleteCodingTunnelProfile: vi.fn(), DeleteWorkspace: vi.fn(), OpenWorkspaceFolder: vi.fn(),
  RegenerateAgentAPIKey: vi.fn(), SaveAgentTunnelProfile: vi.fn(), SaveCodingTunnelProfile: vi.fn(),
  SetAgentEnabled: vi.fn(), UpdateCodexAccessProfile: vi.fn(), UpdateCodexNetworkAccess: vi.fn(), UpdateCodexRemoteGitRewrite: vi.fn(),
  UpdateCodingTunnelNetwork: vi.fn(), UpdateAgentTunnelNetwork: vi.fn(),
}));
vi.mock("../wailsjs/runtime/runtime", () => ({ WindowHide: vi.fn() }));
afterEach(cleanup);

test("ready Coding displays last command timeout and observed phase, then clears after success", async () => {
  fixture.snapshot = { state: "running", coding: { state: "ready", last_execution: {
    state: "failed", error: "CODEX_TOOLHOST_COMMAND_TIMEOUT: deadline",
    diagnostics: { access_profile: "safe", phases: [{ name: "command_exec", state: "failed" }] },
  } } };
  render(<App />);
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("CODEX_TOOLHOST_COMMAND_TIMEOUT");
  expect(alert.textContent).toContain("SAFE · 命令启动或执行");
  fixture.snapshot = { state: "running", coding: { state: "ready", last_execution: { state: "completed" } } };
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull(), { timeout: 2500 });
});

test("unverified ready workspace does not invent an execution failure", async () => {
  fixture.snapshot = { state: "running", coding: { state: "ready" } };
  render(<App />);
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
});

test("successful execution shows large ACL advisory as a branch-scoped warning, not an error", async () => {
  fixture.snapshot = { state: "running", coding: {
    state: "ready", last_execution_branch: "feature/test",
    last_execution: { state: "completed", diagnostics: { access_profile: "safe", warnings: ["WINDOWS_WORKSPACE_ACL_LARGE"] } },
  } };
  render(<App />);
  const status = await screen.findByRole("status");
  expect(status.textContent).toContain("WINDOWS_WORKSPACE_ACL_LARGE");
  expect(status.textContent).toContain("feature/test");
  expect(status.textContent).toContain("工作区权限规则较多");
  expect(screen.queryByRole("alert")).toBeNull();
});

test("idle stale Agent bridge is shown as dormant without a red error", async () => {
  fixture.snapshot = { state: "running", agent_enabled: true, agent: {
    bridge_state: "OFFLINE", active: 0, last_error: "AGENT_BRIDGE_STALE",
  } };
  render(<App />);
  screen.getByRole("tab", { name: /Agent/ }).click();
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  expect(await screen.findByText("已休眠")).toBeTruthy();
});