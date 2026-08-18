import { computed, reactive, ref } from "vue";
import { useRoute } from "vue-router";
import { defineStore } from "pinia";
import { useLoading } from "@sa/hooks";
import { fetchGetUserInfo, fetchLogin } from "@/service/api";
import { useRouterPush } from "@/hooks/common/router";
import { localStg } from "@/utils/storage";
import { SetupStoreId } from "@/enum";
import { $t } from "@/locales";
import { useRouteStore } from "../route";
import { useTabStore } from "../tab";
import { clearAuthStorage, getToken } from "./shared";

export const useAuthStore = defineStore(SetupStoreId.Auth, () => {
  const route = useRoute();
  const authStore = useAuthStore();
  const routeStore = useRouteStore();
  const tabStore = useTabStore();
  const { toLogin, redirectFromLogin } = useRouterPush(false);
  const { loading: loginLoading, startLoading, endLoading } = useLoading();

  const token = ref("");

  const userInfo: Api.Auth.UserInfo = reactive({
    userId: "",
    userName: "",
    roles: [],
    buttons: [],
  });

  /** is super role in static route */
  const isStaticSuper = computed(() => {
    const { VITE_AUTH_ROUTE_MODE, VITE_STATIC_SUPER_ROLE } = import.meta.env;

    return VITE_AUTH_ROUTE_MODE === "static" && userInfo.roles.includes(VITE_STATIC_SUPER_ROLE);
  });

  /** Is login */
  const isLogin = computed(() => Boolean(token.value));

  /** Reset auth store */
  async function resetStore() {
    recordUserId();

    clearAuthStorage();

    authStore.$reset();

    if (!route.meta.constant) {
      await toLogin();
    }

    tabStore.cacheTabs();
    routeStore.resetStore();
  }

  /** Record the user ID of the previous login session Used to compare with the current user ID on next login */
  function recordUserId() {
    if (!userInfo.userId) {
      return;
    }

    // Store current user ID locally for next login comparison
    localStg.set("lastLoginUserId", userInfo.userId);
  }

  /**
   * Check if current login user is different from previous login user If different, clear all tabs
   *
   * @returns {boolean} Whether to clear all tabs
   */
  function checkTabClear(): boolean {
    if (!userInfo.userId) {
      return false;
    }

    const lastLoginUserId = localStg.get("lastLoginUserId");

    // Clear all tabs if current user is different from previous user
    if (!lastLoginUserId || lastLoginUserId !== userInfo.userId) {
      localStg.remove("globalTabs");
      tabStore.clearTabs();

      localStg.remove("lastLoginUserId");
      return true;
    }

    localStg.remove("lastLoginUserId");
    return false;
  }

  /**
   * Login
   *
   * @param userName User name
   * @param password Password
   * @param [redirect=true] Whether to redirect after login. Default is `true`
   */
  async function login(userName: string, password: string, redirect = true) {
    startLoading();

    const { data: loginToken, error } = await fetchLogin(userName, password);

    if (!error) {
      const pass = await loginByToken(loginToken);

      if (pass) {
        // Check if the tab needs to be cleared
        const isClear = checkTabClear();
        let needRedirect = redirect;

        if (isClear) {
          // If the tab needs to be cleared,it means we don't need to redirect.
          needRedirect = false;
        }
        await redirectFromLogin(needRedirect);

        window.$notification?.success({
          title: $t("page.login.common.loginSuccess"),
          content: $t("page.login.common.welcomeBack", { userName: userInfo.userName }),
          duration: 4500,
        });
      }
    } else {
      resetStore();
    }

    endLoading();
  }

  async function loginByToken(loginToken: Api.Auth.LoginToken) {
    // Kratos 返回 snake_case：access_token / refresh_token
    const accessToken = loginToken.access_token;
    const refreshToken = loginToken.refresh_token;

    // 1. stored in the localStorage, the later requests need it in headers
    localStg.set("token", accessToken);
    localStg.set("refreshToken", refreshToken);

    // 2. get user info
    const pass = await getUserInfo();

    if (pass) {
      token.value = accessToken;
      return true;
    }

    return false;
  }

  async function getUserInfo() {
    const { data, error } = await fetchGetUserInfo();

    if (!error) {
      // Kratos 返回 { admin: {...}, permissions: ["catalog:read", ...] }（snake_case）
      const admin = (data as any)?.admin || data;
      userInfo.userId = String(admin?.id || admin?.user_id || "");
      userInfo.userName = admin?.username || admin?.userName || "";
      // 真实权限点（后端 GetProfile 下发）：buttons 供 hasAuth/v-auth 按钮级控制；
      // roles 仅映射超管（R_super 驱动 isStaticSuper 全量放行），非超管为空走权限码过滤。
      // 兼容回退：旧后端无 permissions 字段（undefined≠空数组）→ 按 R_super 全量放行
      // （后端中间件每请求仍独立鉴权，此处仅菜单/按钮表现层回退，不构成越权）
      const rawPerms = (data as any)?.permissions;
      const perms: string[] = Array.isArray(rawPerms) ? rawPerms : ["*"];
      userInfo.buttons = perms;
      userInfo.roles = perms.includes("*") ? ["R_super"] : [];
      return true;
    }

    return false;
  }

  async function initUserInfo() {
    const maybeToken = getToken();

    if (maybeToken) {
      token.value = maybeToken;
      const pass = await getUserInfo();

      if (!pass) {
        resetStore();
      }
    }
  }

  return {
    token,
    userInfo,
    isStaticSuper,
    isLogin,
    loginLoading,
    resetStore,
    login,
    initUserInfo,
  };
});
