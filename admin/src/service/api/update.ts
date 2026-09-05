// 在线更新 API（system:update 超管专属；doc/在线更新方案.md §9）。

import { request } from "../request";

export interface ReleaseNoteEntry {
  version: string;
  channel: string;
  notes: string;
  issued_at: string;
}

export interface UpdateStatus {
  phase: string; // idle|checking|backing_up|downloading|applying|restarting|verifying|rolled_back|failed
  current_version: string;
  target_version: string;
  progress_percent: number;
  error_message: string;
  source: string;
  mode: string;
  supervisor_kind: string;
  has_update: boolean;
  notes: string;
  latest_version: string;
  checked_at: string;
  backup_dir: string;
  busy: boolean;
  history?: ReleaseNoteEntry[];
  backup_ready?: boolean;
  backup_hint?: string;
}

export interface UpdateCheckResult {
  current_version: string;
  latest_version: string;
  has_update: boolean;
  notes: string;
  channel: string;
  source: string;
  history?: ReleaseNoteEntry[];
}

export function fetchUpdateStatus() {
  return request<UpdateStatus>({ url: "/api/v1/admin/update/status" });
}

export function checkUpdate() {
  return request<UpdateCheckResult>({ url: "/api/v1/admin/update/check", method: "post", data: {} });
}

export function applyUpdate() {
  return request<UpdateStatus>({ url: "/api/v1/admin/update/apply", method: "post", data: {} });
}

export function rollbackUpdate() {
  return request<UpdateStatus>({ url: "/api/v1/admin/update/rollback", method: "post", data: {} });
}

// 源配置（settings system/update;保存走通用设置接口）。
export interface UpdateSourceConfig {
  mode: string; // auto | github | accel | static
  repo: string;
  accelerators?: string[];
  static_base: string;
  channel: string;
  supervisor?: string; // auto | systemd | supervisord | none
}

const DEFAULT_CONFIG: UpdateSourceConfig = {
  mode: "auto",
  repo: "NovaWorks/zcard-next",
  accelerators: ["https://gh-proxy.com", "https://ghfast.top", "https://ghproxy.net"],
  static_base: "",
  channel: "stable",
  supervisor: "auto"
};

export async function fetchUpdateSourceConfig(): Promise<UpdateSourceConfig> {
  const { data } = await request<{ items?: Array<{ key: string; value_json: string }> }>({
    url: "/api/v1/admin/settings",
    params: { group: "system" }
  });
  const it = (data?.items || []).find(x => x.key === "update");
  if (!it?.value_json) return { ...DEFAULT_CONFIG };
  try {
    return { ...DEFAULT_CONFIG, ...JSON.parse(it.value_json) };
  } catch {
    return { ...DEFAULT_CONFIG };
  }
}

export function updateUpdateSourceConfig(value: UpdateSourceConfig) {
  return request({
    url: "/api/v1/admin/settings/system/update",
    method: "put",
    data: { value_json: JSON.stringify(value) }
  });
}
