import { request } from "../request";

// ── 支付渠道 ──

export function fetchChannels() {
  return request({ url: "/api/v1/admin/payment/channels" });
}

export function createChannel(data: {
  name: string;
  code: string;
  driver: string;
  config_json: string;
  fee?: number;
  fee_type?: string;
  enabled?: boolean;
}) {
  return request({
    url: "/api/v1/admin/payment/channels",
    method: "post",
    data,
  });
}

export function updateChannel(id: number, data: Record<string, any>) {
  return request({
    url: `/api/v1/admin/payment/channels/${id}`,
    method: "put",
    data,
  });
}

export function deleteChannel(id: number) {
  return request({
    url: `/api/v1/admin/payment/channels/${id}`,
    method: "delete",
  });
}

// ── 员工管理 ──

export function fetchAdmins() {
  return request({ url: "/api/v1/admin/admins" });
}

export function createAdmin(data: {
  username: string;
  password: string;
  nickname?: string;
  role_id: number;
}) {
  return request({
    url: "/api/v1/admin/admins",
    method: "post",
    data,
  });
}

export function updateAdmin(id: number, data: Record<string, any>) {
  return request({
    url: `/api/v1/admin/admins/${id}`,
    method: "put",
    data,
  });
}

export function toggleAdmin(id: number, enabled: boolean) {
  return request({
    url: `/api/v1/admin/admins/${id}/toggle`,
    method: "put",
    data: { enabled },
  });
}

// ── 角色管理 ──

export function fetchRoles() {
  return request({ url: "/api/v1/admin/authz/roles" });
}

export function fetchRole(id: number) {
  return request({ url: `/api/v1/admin/authz/roles/${id}` });
}

export function createRole(data: {
  name: string;
  code: string;
  description?: string;
  permissions?: string[];
}) {
  return request({
    url: "/api/v1/admin/authz/roles",
    method: "post",
    data,
  });
}

export function updateRole(id: number, data: Record<string, any>) {
  return request({
    url: `/api/v1/admin/authz/roles/${id}`,
    method: "put",
    data,
  });
}

export function deleteRole(id: number) {
  return request({
    url: `/api/v1/admin/authz/roles/${id}`,
    method: "delete",
  });
}

export function updateRolePermissions(id: number, permissions: string[]) {
  return request({
    url: `/api/v1/admin/authz/roles/${id}/permissions`,
    method: "put",
    data: { permissions },
  });
}

export function fetchPermissionTree() {
  return request({ url: "/api/v1/admin/authz/permissions" });
}

// ── 系统设置 ──

export function fetchSettings(group?: string) {
  return request({
    url: "/api/v1/admin/settings",
    method: "get",
    params: group ? { group } : undefined,
  });
}

export function updateSetting(group: string, key: string, valueJson: string) {
  return request({
    url: `/api/v1/admin/settings/${group}/${key}`,
    method: "put",
    data: { value_json: valueJson },
  });
}

// ── 货币（P0-04；符号/位置/小数位——前端金额格式化统一取默认货币）──

export function fetchCurrencies() {
  return request({ url: "/api/v1/admin/currencies", method: "get" });
}
