// 按表格容器实际宽度分档（full ≥1080 / mid ≥720 / compact），
// 渠道管理三个 tab 依档裁剪列集：任意屏宽下操作列完整可见、不依赖横向滚动。
import { computed, type Ref } from "vue";
import { useElementSize } from "@vueuse/core";

export type TableTier = "compact" | "mid" | "full";

export function useResponsiveTier(el: Ref<HTMLElement | null>) {
  // 首帧用视口宽估位（桌面优先），ResizeObserver 挂载后随即校正，避免闪现错误档位
  const { width } = useElementSize(el, { width: typeof window === "undefined" ? 1200 : window.innerWidth, height: 0 });
  const tier = computed<TableTier>(() => (width.value >= 1080 ? "full" : width.value >= 720 ? "mid" : "compact"));
  return { tier, containerWidth: width };
}
