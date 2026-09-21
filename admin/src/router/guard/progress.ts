import type { Router } from "vue-router";
import { isAssetLoadError, showAssetLoadError, clearAssetLoadError } from "@/plugins/asset-recovery";

export function createProgressGuard(router: Router) {
  router.beforeEach(() => {
    window.NProgress?.start?.();
    return;
  });
  router.onError((error, to) => {
    window.NProgress?.done?.();
    if (isAssetLoadError(error)) {
      showAssetLoadError(() => window.location.assign(router.resolve(to.fullPath).href));
    }
  });
  router.afterEach((_to, _from, failure) => {
    if (!failure) clearAssetLoadError();
    window.NProgress?.done?.();
  });
}
