import { adminBasePath } from "@/utils/admin-base";
import type { App } from "vue";
import {
  type RouterHistory,
  createMemoryHistory,
  createRouter,
  createWebHashHistory,
  createWebHistory,
} from "vue-router";
import { createBuiltinVueRoutes } from "./routes/builtin";
import { createRouterGuard } from "./guard";

const { VITE_ROUTER_HISTORY_MODE = "history" } = import.meta.env;

const historyCreatorMap: Record<Env.RouterHistoryMode, (base?: string) => RouterHistory> = {
  hash: createWebHashHistory,
  history: createWebHistory,
  memory: createMemoryHistory,
};

export const router = createRouter({
  history: historyCreatorMap[VITE_ROUTER_HISTORY_MODE](adminBasePath()),
  routes: createBuiltinVueRoutes(),
});

/** Setup Vue Router */
export async function setupRouter(app: App) {
  app.use(router);
  createRouterGuard(router);
  await router.isReady();
}
