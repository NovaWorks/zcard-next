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

/** 用户列表（关键词/状态筛选，分页） */
export function fetchUsers(params?: {
  keyword?: string;
  status?: string;
  page?: number;
  page_size?: number;
}) {
  return request<{ users: any[]; total: number }>({
    url: "/api/v1/admin/users",
    method: "get",
    params,
  });
}

/** 封禁/解封用户（identity:user_status，超管专属） */
export function setUserStatus(id: number, status: "active" | "banned") {
  return request<any>({
    url: `/api/v1/admin/users/${id}/status`,
    method: "post",
    data: { status },
  });
}
