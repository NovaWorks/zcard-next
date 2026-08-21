/**
 * 路由 → 后端权限点码映射（authz 声明式目录，server/internal/mods/authz/permissions.go）
 *
 * 设计：不写进 elegant/routes.ts（生成物，重生成会覆盖），而是在路由初始化与
 * 守卫两处按 route.name 查表——缺省（无条目）= 登录即可见。
 * 超管 "*" 通配在判定函数内处理。
 */

/** 路由名 → 所需权限点（任一命中即可见） */
export const ROUTE_PERMISSIONS: Record<string, string[]> = {
  home: ["dashboard:read"],
  product: ["catalog:read"],
  marketing: ["memberlevel:read", "coupon:read", "content:read"],
  inventory: ["inventory:read"],
  order: ["order:read"],
  "payment-channel": ["payment:read"],
  wallet: ["wallet:read"],
  user: ["identity:user_read"],
  staff: ["identity:admin_read"],
  settings: ["settings:read"],
  channel: ["supply:read", "supplier:read", "procurement:read"],
};

/**
 * 判定路由对当前权限点集是否可见
 *
 * @param routeName 路由名（elegant-router name）
 * @param buttons 当前用户权限点集（profile.permissions，超管含 "*"）
 */
export function hasRoutePermission(routeName: string, buttons: string[]): boolean {
  const required = ROUTE_PERMISSIONS[routeName];
  if (!required?.length) {
    return true;
  }

  if (buttons.includes("*")) {
    return true;
  }

  return required.some((code) => buttons.includes(code));
}
