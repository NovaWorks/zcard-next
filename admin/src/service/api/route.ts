/**
 * 路由数据源（本地派生——后端无 /route/* 端点）
 *
 * soybean 模板原指向 mock 接口 `/route/getUserRoutes` 等，后端 Go 侧从未实现，
 * 切到 dynamic 模式会直接失败登出。此处改为**本地派生**：静态路由表 + 登录用户
 * 的权限点（profile.permissions）在客户端过滤——static/dynamic 两模式均可用，
 * 且菜单口径一致（ROUTE_PERMISSIONS 单一真理源）。
 *
 * ⚠️ 依赖必须走函数内动态 import：service/api 与 @/router/routes、store 之间存在
 * 静态环（store → service/api → router/routes → builtin → elegant/imports），
 * 顶层静态 import 会打乱模块求值顺序（"Cannot access 'layouts' before
 * initialization" 白屏事故，2026-08-18），动态 import 在调用期才加载、彻底断环。
 */
import type { ElegantConstRoute, LastLevelRouteKey } from "@elegant-router/types";

/** get constant routes */
export async function fetchGetConstantRoutes() {
  const { createStaticRoutes } = await import("@/router/routes");
  const { constantRoutes } = createStaticRoutes();
  return { data: constantRoutes as Api.Route.MenuRoute[], error: null };
}

/** get user routes（按当前登录用户的角色与权限点过滤；超管全量） */
export async function fetchGetUserRoutes() {
  const [{ createStaticRoutes }, { useAuthStore }, shared] = await Promise.all([
    import("@/router/routes"),
    import("@/store/modules/auth"),
    import("@/store/modules/route/shared"),
  ]);

  const { authRoutes } = createStaticRoutes();
  const authStore = useAuthStore();

  const filtered = authStore.isStaticSuper
    ? authRoutes
    : shared.filterAuthRoutesByPermissions(
        shared.filterAuthRoutesByRoles(authRoutes, authStore.userInfo.roles),
        authStore.userInfo.buttons,
      );

  const routes = shared.sortRoutesByOrder(shared.filterRoutesByDev(filtered));
  const firstVisible = routes.find((route) => !route.meta?.hideInMenu);

  return {
    data: {
      routes: routes as Api.Route.MenuRoute[],
      home: (firstVisible?.name ?? "home") as LastLevelRouteKey,
    },
    error: null,
  };
}

/**
 * whether the route is exist
 *
 * @param routeName route name
 */
export async function fetchIsRouteExist(routeName: string) {
  const { createStaticRoutes } = await import("@/router/routes");
  const { authRoutes } = createStaticRoutes();
  const names = new Set<string>();
  const walk = (routes: ElegantConstRoute[]) => {
    routes.forEach((route) => {
      names.add(route.name as string);
      if (route.children?.length) walk(route.children);
    });
  };
  walk(authRoutes);
  return { data: names.has(routeName), error: null };
}
