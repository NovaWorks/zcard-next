import type { App, Directive } from "vue";
import { useAuthStore } from "@/store/modules/auth";

/**
 * 权限点判定（与后端 authz.Allowed 同语义：任一命中或 "*" 通配即通过）
 *
 * 供 v-auth 指令与脚本侧调用；store 依赖 pinia 已安装（main.ts 中 setupStore 先于 mount）
 */
export function checkAuth(codes: string | string[]): boolean {
  const authStore = useAuthStore();

  if (!authStore.isLogin) {
    return false;
  }

  const buttons = authStore.userInfo.buttons;

  if (buttons.includes("*")) {
    return true;
  }

  return typeof codes === "string" ? buttons.includes(codes) : codes.some((c) => buttons.includes(c));
}

/**
 * v-auth="'catalog:write'" / v-auth="['order:cancel', 'order:refund']"
 *
 * 无权限时移除元素（DOM 级隐藏——比 disabled 更强的表达：无权者不知道该功能存在）
 */
export const authDirective: Directive<HTMLElement, string | string[]> = {
  mounted(el, binding) {
    if (!binding.value) {
      return;
    }

    if (!checkAuth(binding.value)) {
      el.parentNode?.removeChild(el);
    }
  },
};

/** 注册全局指令 */
export function setupDirectives(app: App) {
  app.directive("auth", authDirective);
}
