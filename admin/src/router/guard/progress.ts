import type { Router } from "vue-router";

export function createProgressGuard(router: Router) {
  router.beforeEach(() => {
    window.NProgress?.start?.();
    return;
  });
  router.onError(() => { window.NProgress?.done?.(); });
  router.afterEach(() => {
    window.NProgress?.done?.();
  });
}
