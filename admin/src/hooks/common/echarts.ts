import { computed, effectScope, nextTick, onScopeDispose, shallowRef, watch } from "vue";
import { useElementSize } from "@vueuse/core";
import type * as echarts from "echarts/core";
import type {
  BarSeriesOption,
  GaugeSeriesOption,
  LineSeriesOption,
  PictorialBarSeriesOption,
  PieSeriesOption,
  RadarSeriesOption,
  ScatterSeriesOption,
} from "echarts/charts";
import type {
  DatasetComponentOption,
  GridComponentOption,
  LegendComponentOption,
  TitleComponentOption,
  ToolboxComponentOption,
  TooltipComponentOption,
} from "echarts/components";
import { useThemeStore } from "@/store/modules/theme";

export type ECOption = echarts.ComposeOption<
  | BarSeriesOption
  | LineSeriesOption
  | PieSeriesOption
  | ScatterSeriesOption
  | PictorialBarSeriesOption
  | RadarSeriesOption
  | GaugeSeriesOption
  | TitleComponentOption
  | LegendComponentOption
  | TooltipComponentOption
  | GridComponentOption
  | ToolboxComponentOption
  | DatasetComponentOption
>;

interface ChartHooks {
  onRender?: (chart: echarts.ECharts) => void | Promise<void>;
  onUpdated?: (chart: echarts.ECharts) => void | Promise<void>;
  onDestroy?: (chart: echarts.ECharts) => void | Promise<void>;
}

/**
 * use echarts
 *
 * @param optionsFactory echarts options factory function
 * @param darkMode dark mode
 */
export function useEcharts<T extends ECOption>(optionsFactory: () => T, hooks: ChartHooks = {}) {
  const scope = effectScope();
  let disposed = false;

  const themeStore = useThemeStore();
  const darkMode = computed(() => themeStore.darkMode);

  const domRef = shallowRef<HTMLElement | null>(null);
  const initialSize = { width: 0, height: 0 };
  const { width, height } = useElementSize(domRef, initialSize);

  const chart = shallowRef<echarts.ECharts | null>(null);
  const chartOptions: T = optionsFactory();

  const {
    onRender = (instance) => {
      const textColor = darkMode.value ? "rgb(224, 224, 224)" : "rgb(31, 31, 31)";
      const maskColor = darkMode.value ? "rgba(0, 0, 0, 0.4)" : "rgba(255, 255, 255, 0.8)";

      instance.showLoading({
        color: themeStore.themeColor,
        textColor,
        fontSize: 14,
        maskColor,
      });
    },
    onUpdated = (instance) => {
      instance.hideLoading();
    },
    onDestroy,
  } = hooks;

  /** is chart rendered */
  function isRendered() {
    return Boolean(domRef.value && chart.value);
  }

  /**
   * update chart options
   *
   * @param callback callback function
   */
  async function updateOptions(
    callback: (opts: T, optsFactory: () => T) => ECOption = () => chartOptions,
  ) {
    const updatedOpts = callback(chartOptions, optionsFactory);

    Object.assign(chartOptions, updatedOpts);

    await nextTick();

    if (!isRendered()) return;

    if (isRendered()) {
      chart.value?.clear();
    }

    chart.value?.setOption({ ...updatedOpts, backgroundColor: "transparent" });

    await onUpdated?.(chart.value!);
  }

  function setOptions(options: T) {
    chart.value?.setOption(options);
  }

  /** render chart */
  async function render() {
    if (disposed || !domRef.value || isRendered()) return;
    const { echarts } = await import("./echarts-runtime");
    // Several charts/resize events can finish the shared import together.
    if (disposed || !domRef.value || isRendered()) return;

    const chartTheme = darkMode.value ? "dark" : "light";

    chart.value = echarts.init(domRef.value, chartTheme);

    chart.value?.setOption({ ...chartOptions, backgroundColor: "transparent" });

    await onRender?.(chart.value!);
  }

  /** resize chart */
  function resize() {
    chart.value?.resize();
  }

  /** destroy chart */
  async function destroy() {
    if (!chart.value) return;

    await onDestroy?.(chart.value);
    chart.value?.dispose();
    chart.value = null;
  }

  /** change chart theme */
  async function changeTheme() {
    await destroy();
    await render();
    if (chart.value) await onUpdated?.(chart.value);
  }

  /**
   * render chart by size
   *
   * @param w width
   * @param h height
   */
  async function renderChartBySize(w: number, h: number) {
    initialSize.width = w;
    initialSize.height = h;

    // resize chart
    if (isRendered()) {
      resize();

      return;
    }

    // render chart
    await render();

    if (chart.value) {
      await onUpdated?.(chart.value);
    }
  }

  scope.run(() => {
    watch(
      [width, height],
      ([newWidth, newHeight]) => {
        renderChartBySize(newWidth, newHeight);
      },
      { flush: "post" },
    );

    watch(darkMode, () => {
      changeTheme();
    });
  });

  onScopeDispose(() => {
    disposed = true;
    destroy();
    scope.stop();
  });

  return {
    domRef,
    chart,
    updateOptions,
    setOptions,
  };
}
