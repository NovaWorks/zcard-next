import { request } from '../request';

/** 管理员登录（Kratos：POST /api/v1/admin/auth/login） */
export function fetchLogin(username: string, password: string) {
  return request<Api.Auth.LoginToken>({
    url: '/api/v1/admin/auth/login',
    method: 'post',
    data: { username, password }
  });
}

/** 获取当前用户信息（Kratos：GET /api/v1/admin/auth/profile） */
export function fetchGetUserInfo() {
  return request<Api.Auth.UserInfo>({
    url: '/api/v1/admin/auth/profile'
  });
}

/** 刷新令牌（Kratos：POST /api/v1/admin/auth/refresh） */
export function fetchRefreshToken(refreshToken: string) {
  return request<Api.Auth.LoginToken>({
    url: '/api/v1/admin/auth/refresh',
    method: 'post',
    data: { refresh_token: refreshToken }
  });
}

/** 登出 */
export function fetchLogout(refreshToken: string) {
  return request({
    url: '/api/v1/admin/auth/logout',
    method: 'post',
    data: { refresh_token: refreshToken }
  });
}
