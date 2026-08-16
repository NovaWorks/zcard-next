declare namespace Api {
  namespace Auth {
    /** Kratos 登录响应（snake_case） */
    interface LoginToken {
      access_token: string;
      refresh_token: string;
      token_type: string;
      expires_at: number;
      admin: AdminProfile;
    }

    /** Kratos 管理员信息 */
    interface AdminProfile {
      id: number;
      username: string;
      nickname: string;
      avatar: string;
      role_id: number;
      totp_enabled: boolean;
      last_login_ip: string;
    }

    /** Kratos profile 响应 */
    interface UserInfo {
      admin?: AdminProfile;
      permissions?: string[];
      /** 运行时会话字段（login 后填充） */
      userId: string;
      userName: string;
      roles: string[];
      buttons: string[];
    }
  }
}
