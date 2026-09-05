import { request } from "../request";

/** 管理员登录（Kratos：POST /api/v1/admin/auth/login；captcha_admin_login 开启时带验证码） */
export function fetchLogin(username: string, password: string, captcha?: { captcha_id: string; captcha_code: string }) {
  return request<Api.Auth.LoginToken>({
    url: "/api/v1/admin/auth/login",
    method: "post",
    data: { username, password, ...captcha },
  });
}

/** 登录图形验证码（免鉴权；GET /api/v1/admin/auth/captcha/image） */
export function fetchAdminCaptchaImage() {
  return request<{ captcha_id: string; image_base64: string }>({
    url: "/api/v1/admin/auth/captcha/image",
  });
}

/** 登录验证码开关（免鉴权；GET /api/v1/admin/auth/captcha-config） */
export function fetchAdminCaptchaConfig() {
  return request<{ enabled: boolean }>({
    url: "/api/v1/admin/auth/captcha-config",
  });
}

/** 获取当前用户信息（Kratos：GET /api/v1/admin/auth/profile） */
export function fetchGetUserInfo() {
  return request<Api.Auth.UserInfo>({
    url: "/api/v1/admin/auth/profile",
  });
}

/** 刷新令牌（Kratos：POST /api/v1/admin/auth/refresh） */
export function fetchRefreshToken(refreshToken: string) {
  return request<Api.Auth.LoginToken>({
    url: "/api/v1/admin/auth/refresh",
    method: "post",
    data: { refresh_token: refreshToken },
  });
}

/** 登出 */
export function fetchLogout(refreshToken: string) {
  return request({
    url: "/api/v1/admin/auth/logout",
    method: "post",
    data: { refresh_token: refreshToken },
  });
}

// ── 前台用户管理（identity:user_read / identity:user_status）──

/** 用户列表（关键词/状态/供货商筛选，分页；含等级/钱包/订单/供货聚合） */
export function fetchUsers(params?: {
  keyword?: string;
  status?: string;
  is_supplier?: boolean;
  page?: number;
  page_size?: number;
}) {
  return request<{ users: any[]; total: number }>({
    url: "/api/v1/admin/users",
    method: "get",
    params,
  });
}

/** 用户详情（聚合：等级/钱包/优惠券/供货账户/邀请关系/最近订单） */
export function fetchUserDetail(id: number) {
  return request<any>({ url: `/api/v1/admin/users/${id}` });
}

/** 新增用户（identity:user_create，超管） */
export function createUser(data: { username: string; password: string; email?: string }) {
  return request<any>({ url: "/api/v1/admin/users", method: "post", data });
}

/** 重置用户密码（identity:user_reset_pwd，超管） */
export function resetUserPassword(id: number, newPassword: string) {
  return request({ url: `/api/v1/admin/users/${id}/password`, method: "put", data: { new_password: newPassword } });
}

/** 封禁/解封用户（identity:user_status，超管专属） */
export function setUserStatus(id: number, status: "active" | "banned") {
  return request<any>({
    url: `/api/v1/admin/users/${id}/status`,
    method: "post",
    data: { status },
  });
}
