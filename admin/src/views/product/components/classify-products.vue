<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { NAlert, NButton, NInput, NModal, NSelect, NSpace, NTag } from "naive-ui";
import { classifyProducts } from "@/service/api/catalog";
const props = defineProps<{ show: boolean; categories: { label: string; value: number }[] }>();
const emit = defineEmits<{ (e: "update:show", v: boolean): void; (e: "saved"): void }>();
const form = reactive({
  keywords: "",
  excludes: "",
  match_all: false,
  category_id: null as number | null,
});
const result = ref<any>(null),
  busy = ref(false),
  error = ref(""),
  applied = ref(false);
const words = (s: string) => [
  ...new Set(
    s
      .split(/[,，;；\n]+/)
      .map((v) => v.trim())
      .filter(Boolean),
  ),
];
const target = computed(
  () => props.categories.find((c) => c.value === form.category_id)?.label || "",
);
watch(form, () => {
  result.value = null;
  applied.value = false;
});
watch(
  () => props.show,
  (v) => {
    if (v) {
      result.value = null;
      error.value = "";
      applied.value = false;
    }
  },
);
async function run(apply = false) {
  if (busy.value || !form.category_id || !words(form.keywords).length) return;
  busy.value = true;
  error.value = "";
  try {
    const { data, error: e } = await classifyProducts({
      keywords: words(form.keywords),
      excludes: words(form.excludes),
      match_all: form.match_all,
      category_id: form.category_id,
      apply,
      revisions: apply
        ? Object.fromEntries((result.value?.items || []).map((p: any) => [p.id, p.revision]))
        : {},
    });
    if (e) {
      error.value = (e as any)?.response?.data?.message || "处理失败，请重试；设置已保留";
      return;
    }
    result.value = data;
    applied.value = apply;
    if (apply) emit("saved");
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <NModal
    :show="show"
    preset="card"
    title="按关键词自动分类"
    style="width: 740px; max-width: 96vw"
    :mask-closable="!busy"
    :closable="!busy"
    :close-on-esc="!busy"
    @update:show="(v) => emit('update:show', v)"
  >
    <div class="classify-products">
      <NAlert type="info"
        >范围：本站全部商品，包含已下架商品。按商品名称匹配；锁定商品、已手动指定分类的商品会跳过。先预览，再确认修改。</NAlert
      >
      <label
        >包含关键词（逗号分隔）<NInput
          v-model:value="form.keywords"
          :disabled="busy"
          placeholder="例如：小火箭，Shadowrocket，Apple ID"
      /></label>
      <NSelect
        :value="form.match_all ? 1 : 0"
        @update:value="(v) => (form.match_all = Boolean(v))"
        :disabled="busy"
        :options="[
          { label: '包含任意一个关键词', value: 0 },
          { label: '必须包含所有关键词', value: 1 },
        ]"
      />
      <label
        >排除关键词（可不填）<NInput
          v-model:value="form.excludes"
          :disabled="busy"
          placeholder="例如：独享，充值"
      /></label>
      <label
        >分到哪个分类<NSelect
          v-model:value="form.category_id"
          :disabled="busy"
          :options="categories"
          filterable
          placeholder="选择目标分类"
      /></label>
      <NAlert v-if="error" type="error">{{ error }}</NAlert>
      <template v-if="result">
        <NAlert v-if="result.truncated" type="warning"
          >匹配超过1000件，请补充关键词缩小范围后重新预览。</NAlert
        >
        <NAlert v-else :type="applied ? 'success' : 'info'"
          >{{
            applied
              ? `已修改 ${result.updated || 0} 件`
              : `将修改 ${result.items?.length || 0} 件，移至「${target}」`
          }}；跳过锁定 {{ result.skipped_locked || 0 }} 件，保留手动分类
          {{ result.skipped_protected || 0 }} 件，已在目标分类
          {{ result.unchanged || 0 }} 件。</NAlert
        >
        <div class="classification-preview">
          <div v-for="p in result.items" :key="p.id" class="classification-row">
            <span>{{ p.name }}</span
            ><NTag :type="p.status === 'conflict' ? 'warning' : 'default'">{{
              p.status === "conflict"
                ? "设置已变化，跳过"
                : p.status === "updated"
                  ? "已修改"
                  : p.status === "protected"
                    ? "保留手动分类"
                    : target
            }}</NTag>
          </div>
        </div>
        <p v-if="applied">
          仅本次预览中的商品会被修改。今后导入的新商品，可在导入窗口保存关键词分类规则。
        </p>
      </template>
    </div>
    <template #footer
      ><NSpace justify="end"
        ><NButton :disabled="busy" @click="emit('update:show', false)">关闭</NButton
        ><NButton
          :loading="busy && !applied"
          :disabled="!form.category_id || !words(form.keywords).length"
          @click="run(false)"
          >预览匹配商品</NButton
        ><NButton
          v-if="result && !applied"
          type="primary"
          :disabled="busy || result.truncated || !result.items?.length"
          @click="run(true)"
          >确认分类 {{ result.items?.length || 0 }} 件商品</NButton
        ></NSpace
      ></template
    >
  </NModal>
</template>
<style scoped>
.classify-products {
  display: grid;
  gap: 12px;
  max-height: 68dvh;
  overflow: auto;
}
.classify-products label {
  display: grid;
  gap: 6px;
}
.classification-preview {
  max-height: 280px;
  overflow: auto;
}
.classification-row {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 0;
  overflow-wrap: anywhere;
}
.classification-row span {
  min-width: 0;
}
</style>
