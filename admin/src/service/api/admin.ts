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
  icon?: string;
  methods_json?: string;
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

export function resetAdminPassword(id: number, password: string) {
  return request({
    url: `/api/v1/admin/admins/${id}/reset-password`,
    method: "put",
    data: { password },
  });
}

export function resetAdminTOTP(id: number) {
  return request({
    url: `/api/v1/admin/admins/${id}/totp-reset`,
    method: "put",
    data: {},
  });
}

export function deleteAdmin(id: number) {
  return request({
    url: `/api/v1/admin/admins/${id}`,
    method: "delete",
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

// 批量更新（表单级保存；后端单事务原子写入）
export function updateSettings(items: { group: string; key: string; value_json: string }[]) {
  return request<{ updated: number }>({
    url: "/api/v1/admin/settings",
    method: "put",
    data: { items },
  });
}

// 可用模板清单（WP 主题式选择；settings.template.pc_template 等取值）
export interface TemplateItem {
  key: string;
  name: string;
  desc: string;
  preview: string;
  author: string;
  version: string;
}

export function fetchTemplates() {
  return request<{ templates: TemplateItem[] }>({
    url: "/api/v1/admin/settings/templates",
    method: "get",
  });
}

// 安装主题（zip base64；服务端解压校验后原子落盘）
export function installTemplate(dataBase64: string) {
  return request<TemplateItem>({
    url: "/api/v1/admin/settings/templates/install",
    method: "post",
    data: { data_base64: dataBase64 },
  });
}

// ── 货币（P0-04；符号/位置/小数位——前端金额格式化统一取默认货币）──

export function fetchCurrencies() {
  return request({ url: "/api/v1/admin/currencies", method: "get" });
}

// ── 支付驱动元数据（P2-09 T5：配置面动态表单渲染）──

// ── 支付单（payment:read_detail / payment:capture）──

export function fetchPayments(params: { status?: string; order_no?: string; cursor?: number; limit?: number }) {
  return request({ url: "/api/v1/admin/payments", params });
}

export function capturePayment(id: number) {
  return request({ url: `/api/v1/admin/payments/${id}/capture`, method: "post", data: {} });
}

export function fetchDrivers() {
  return request({ url: "/api/v1/admin/payment/drivers" });
}

export function fetchFieldOptions(code: string, field: string, configJson: string) {
  return request({
    url: `/api/v1/admin/payment/drivers/${code}/field-options`,
    method: "get",
    params: { field, config_json: configJson },
  });
}
