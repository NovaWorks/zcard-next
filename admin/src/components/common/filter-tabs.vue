<script setup lang="ts">
/**
 * 列表快捷筛选卡片（GitHub issues / Stripe 筛选 pill 交互）：
 * 状态等低基数维度一点即筛，替代下拉的「点开-选择-收起」三步；选中项以主题色
 * 胶囊高亮，语义色圆点常显（Linear/Notion 状态点惯例）；counts 可选展示计数。
 * 语义色取 Naive 主题变量（跟随主题配置，非硬编码）；change 统一触发重查。
 */
import { computed } from "vue";
import { useThemeVars } from "naive-ui";

interface FilterTabOption {
  label: string;
  value: string | number;
  /** 语义色（状态圆点 + 选中态描边/文字）；缺省用主题色 */
  type?: "default" | "primary" | "success" | "warning" | "error" | "info";
}

const props = withDefaults(
  defineProps<{
    value: string | number;
    options: FilterTabOption[];
    /** value 字符串键 → 条数（客户端筛选场景可传，服务端无计数接口省略） */
    counts?: Record<string, number>;
    size?: "small" | "medium";
  }>(),
  { counts: undefined, size: "medium" },
);

const emit = defineEmits<{
  (e: "update:value", v: string | number): void;
  (e: "change", v: string | number): void;
}>();

const vars = useThemeVars();

const typeColor = computed<Record<string, string>>(() => ({
  default: vars.value.primaryColor,
  primary: vars.value.primaryColor,
  success: vars.value.successColor,
  warning: vars.value.warningColor,
  error: vars.value.errorColor,
  info: vars.value.infoColor,
}));

function colorOf(opt: FilterTabOption): string {
  return typeColor.value[opt.type ?? "primary"] || vars.value.primaryColor;
}

// 选中态：主题色描边 + 文字 + 同色 10% 洗底（hex → rgba，避免硬编码色值）
function styleOf(opt: FilterTabOption) {
  if (opt.value !== props.value) return undefined;
  const c = colorOf(opt);
  return { color: c, borderColor: c, background: withAlpha(c, 0.1) };
}

function withAlpha(hex: string, alpha: number): string {
  const m = hex.replace("#", "");
  if (m.length !== 6) return "transparent";
  const r = parseInt(m.slice(0, 2), 16);
  const g = parseInt(m.slice(2, 4), 16);
  const b = parseInt(m.slice(4, 6), 16);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

function pick(v: string | number) {
  if (v === props.value) return;
  emit("update:value", v);
  emit("change", v);
}
</script>

<template>
  <div class="flex flex-wrap items-center gap-4px" role="tablist">
    <button
      v-for="opt in options"
      :key="String(opt.value)"
      type="button"
      role="tab"
      :aria-selected="opt.value === value"
      class="ft"
      :class="{ 'ft--sm': size === 'small', 'ft--on': opt.value === value }"
      :style="styleOf(opt)"
      @click="pick(opt.value)"
    >
      <span class="ft__dot" :style="{ background: colorOf(opt) }" />
      <span>{{ opt.label }}</span>
      <span v-if="counts?.[String(opt.value)] !== undefined" class="ft__count">{{ counts[String(opt.value)] }}</span>
    </button>
  </div>
</template>

<style scoped>
.ft {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 30px;
  padding: 0 13px;
  border: 1px solid transparent;
  border-radius: 999px;
  font-size: 13px;
  line-height: 1;
  color: #64748b;
  background: transparent;
  cursor: pointer;
  transition:
    color 0.2s ease,
    background-color 0.2s ease,
    border-color 0.2s ease;
}
.ft:hover {
  background: rgba(100, 116, 139, 0.12);
}
.ft--on {
  font-weight: 500;
  cursor: default;
}
.ft:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px rgba(100, 116, 139, 0.25);
}
.ft--sm {
  height: 26px;
  padding: 0 11px;
  font-size: 12px;
}
.ft__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}
.ft__count {
  font-size: 12px;
  font-weight: 400;
  opacity: 0.75;
  font-variant-numeric: tabular-nums;
}

.dark .ft {
  color: #94a3b8;
}
.dark .ft:hover {
  background: rgba(148, 163, 184, 0.15);
}
</style>
