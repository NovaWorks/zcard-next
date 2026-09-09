import { nextTick, onActivated, onDeactivated } from 'vue';
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router';

/** 与缓存的目录实例一起保留位置，等 Suspense/KeepAlive 恢复内容后再滚动。 */
export function useCatalogScroll() {
  let position = { left: 0, top: 0 };
  let active = false;
  const save = () => {
    position = { left: window.scrollX, top: window.scrollY };
  };
  onBeforeRouteLeave(save);
  onBeforeRouteUpdate(save);
  onActivated(async () => {
    active = true;
    await nextTick();
    if (active) window.scrollTo(position);
  });
  onDeactivated(() => { active = false; });
}
