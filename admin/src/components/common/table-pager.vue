<script setup lang="ts">
/**
 * 可复用远程分页条（所有列表共用）：
 *   offset 模式（默认）：共 N 条 / M 页 + 首页« + 页码/上一页/下一页/每页条数/跳页 + 末页»
 *   cursor 模式（订单/流水等游标分页——架构禁 OFFSET 深翻页）：首页« + 上一页/下一页 +
 *     第 N 页 + 每页条数（跳页/末页与游标语义冲突，不提供；hasMore 控制下一页可用）
 * change 事件统一回调（page 与 pageSize 任一变化都触发；改条数自动回第 1 页）。
 */
import { computed } from "vue";
import { NButton, NPagination, NSelect } from "naive-ui";

const props = withDefaults(
  defineProps<{
    page: number;
    pageSize: number;
    /** offset 模式必填总条数；cursor 模式忽略 */
    total?: number;
    /** cursor 模式：是否还有下一页 */
    hasMore?: boolean;
    mode?: "offset" | "cursor";
    pageSizes?: number[];
  }>(),
  { total: 0, hasMore: false, mode: "offset", pageSizes: () => [10, 20, 50, 100] },
);

const emit = defineEmits<{
  (e: "update:page", v: number): void;
  (e: "update:page-size", v: number): void;
  (e: "change", page: number, size: number): void;
}>();

const pageCount = computed(() =>
  props.mode === "offset" ? Math.max(1, Math.ceil(props.total / props.pageSize)) : 0,
);

function changePage(p: number) {
  if (p === props.page) return;
  emit("update:page", p);
  emit("change", p, props.pageSize);
}

function changeSize(s: number) {
  // 改每页条数回第 1 页（尾页数据不足时停留旧页会空白）
  emit("update:page-size", s);
  emit("update:page", 1);
  emit("change", 1, s);
}

function go(p: number) {
  if (props.mode === "offset") {
    changePage(Math.min(Math.max(1, p), pageCount.value));
  } else {
    changePage(Math.max(1, p));
  }
}
</script>

<template>
  <div class="mt-12px flex flex-wrap items-center justify-between gap-8px">
    <span class="text-12px text-gray-400">
      {{ mode === "offset" ? `共 ${total} 条 · ${pageCount} 页` : `第 ${page} 页` }}
    </span>
    <div class="flex items-center gap-4px">
      <NButton size="small" quaternary :disabled="page <= 1" title="第一页" @click="go(1)"
        >«</NButton
      >

      <!-- offset：完整分页（页码/每页条数/跳页） -->
      <NPagination
        v-if="mode === 'offset'"
        :page="page"
        :page-size="pageSize"
        :item-count="total"
        :page-sizes="pageSizes"
        show-size-picker
        show-quick-jumper
        size="small"
        @update:page="changePage"
        @update:page-size="changeSize"
      />

      <!-- cursor：上一页/下一页（页码链） + 每页条数 -->
      <template v-else>
        <NButton size="small" quaternary :disabled="page <= 1" title="上一页" @click="go(page - 1)"
          >‹</NButton
        >
        <span class="px-4px text-13px">{{ page }}</span>
        <NButton size="small" quaternary :disabled="!hasMore" title="下一页" @click="go(page + 1)"
          >›</NButton
        >
        <NSelect
          :value="pageSize"
          size="small"
          class="w-100px"
          :options="pageSizes.map((s: number) => ({ label: `${s} 条/页`, value: s }))"
          :consistent-menu-width="false"
          @update:value="changeSize"
        />
      </template>

      <NButton
        v-if="mode === 'offset'"
        size="small"
        quaternary
        :disabled="page >= pageCount"
        title="最后一页"
        @click="go(pageCount)"
      >
        »
      </NButton>
    </div>
  </div>
</template>
