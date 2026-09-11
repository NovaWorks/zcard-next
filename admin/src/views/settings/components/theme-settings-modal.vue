<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  NModal,
  NButton,
  NSelect,
  NInput,
  NInputNumber,
  NSwitch,
  NColorPicker,
  NAlert,
  NSpin,
  NTag,
  NEmpty,
  NPopconfirm,
} from "naive-ui";
import MediaField from "@/components/common/media-picker/media-field.vue";
import {
  fetchThemeSettings,
  saveThemeSettings,
  previewThemeSettings,
} from "@/service/api/theme-settings";
import { fetchProducts, fetchCategories } from "@/service/api/catalog";
import { checkAuth } from "@/directives";
type Field = {
  key: string;
  label: string;
  type: string;
  default: any;
  help?: string;
  min?: number;
  max?: number;
  options?: { label: string; value: string }[];
  visible_when?: { field: string; equals: any };
  capability?: string;
};
type State = {
  key: string;
  theme_revision: string;
  revision: string;
  schema: {
    version: number;
    groups: { id: string; label: string; fields: Field[] }[];
  } | null;
  values: Record<string, any>;
  published?: { saved_at: number };
  draft?: unknown;
  can_rollback: boolean;
  capabilities: Record<string, boolean>;
  active: boolean;
  versions: { revision: string; version: string }[];
};
const props = defineProps<{ show: boolean; themeKey: string }>();
const emit = defineEmits<{
  (e: "update:show", v: boolean): void;
  (e: "published"): void;
}>();
const state = ref<State | null>(null),
  values = ref<Record<string, any>>({}),
  baseline = ref(""),
  group = ref(""),
  loading = ref(false),
  saving = ref(false),
  error = ref("");
const previewURL = ref(""),
  previewBusy = ref(false),
  size = ref("desktop"),
  live = ref(false);
let sequence = 0,
  previewSequence = 0,
  timer: ReturnType<typeof setTimeout> | undefined;
const dirty = computed(() => JSON.stringify(values.value) !== baseline.value);
const canWrite = computed(() => checkAuth("settings:update"));
const fields = computed(
  () =>
    state.value?.schema?.groups.find((g) => g.id === group.value)?.fields || [],
);
const choices = ref<{ label: string; value: number }[]>([]),
  choicesBusy = ref(false);
let choiceSequence = 0;
async function searchChoices(type: string, keyword = "") {
  const seq = ++choiceSequence;
  choicesBusy.value = true;
  const { data, error: err } = await (type === "product"
    ? fetchProducts({ keyword, page: 1, page_size: 50 })
    : fetchCategories());
  if (seq !== choiceSequence) return;
  choicesBusy.value = false;
  if (err) {
    error.value = "读取商品或分类失败，请重试";
    return;
  }
  const rows = (data as any)?.items || (data as any)?.categories || [];
  choices.value = rows.map((v: any) => ({
    value: Number(v.id),
    label: `${v.name}（${v.id}）`,
  }));
}
const versions = computed(
  () =>
    state.value?.versions.map((v) => ({
      label: v.version,
      value: v.revision,
    })) || [],
);
const frameWidth = computed(() =>
  size.value === "mobile"
    ? "390px"
    : size.value === "tablet"
      ? "768px"
      : "100%",
);
function accept(raw: string) {
  state.value = JSON.parse(raw);
  values.value = { ...state.value!.values };
  baseline.value = JSON.stringify(values.value);
  if (!state.value?.schema?.groups.some((g) => g.id === group.value))
    group.value = state.value?.schema?.groups[0]?.id || "";
}
async function load(revision = "") {
  const seq = ++sequence;
  loading.value = true;
  error.value = "";
  const { data, error: err } = await fetchThemeSettings(
    props.themeKey,
    revision,
  );
  if (seq !== sequence) return;
  loading.value = false;
  if (err || !data) {
    error.value = "读取主题设置失败，请重试";
    return;
  }
  accept(data.state_json);
  previewURL.value = "";
  live.value = false;
}
watch(
  () => [props.show, props.themeKey] as const,
  ([show]) => {
    if (show) load();
    else {
      sequence++;
      previewSequence++;
      clearTimeout(timer);
      live.value = false;
      previewURL.value = "";
    }
  },
);
function payload(action: string) {
  return {
    action,
    theme_revision: state.value!.theme_revision,
    expected_revision: state.value!.revision,
    values_json: JSON.stringify(values.value),
  };
}
async function save(action: string) {
  if (!state.value || saving.value) return;
  saving.value = true;
  error.value = "";
  const { data, error: err } = await saveThemeSettings(
    props.themeKey,
    payload(action),
  );
  saving.value = false;
  if (err || !data) {
    error.value =
      "保存失败；若其他页面已修改，请重新加载后再编辑。当前输入已保留。";
    return;
  }
  accept(data.state_json);
  window.$message?.success(
    action === "publish"
      ? "主题设置已发布"
      : action === "rollback"
        ? "已恢复上一版"
        : action === "reset"
          ? "默认设置已存为草稿"
          : "草稿已保存",
  );
  if (action === "publish" || action === "rollback") emit("published");
  if (live.value) preview();
}
async function preview() {
  if (!state.value || !canWrite.value) return;
  const seq = ++previewSequence;
  previewBusy.value = true;
  const { data, error: err } = await previewThemeSettings(
    props.themeKey,
    payload("preview"),
  );
  if (seq !== previewSequence) return;
  previewBusy.value = false;
  if (err || !data) {
    error.value = "预览生成失败，请检查输入或重新加载设置";
    return;
  }
  previewURL.value = data.url;
  live.value = true;
}
watch(
  values,
  () => {
    if (live.value) {
      clearTimeout(timer);
      timer = setTimeout(preview, 700);
    }
  },
  { deep: true },
);
function visible(field: Field) {
  return (
    !field.visible_when ||
    values.value[field.visible_when.field] === field.visible_when.equals
  );
}
function blocked(field: Field) {
  return (
    !!field.capability && state.value?.capabilities[field.capability] === false
  );
}
function close() {
  if (dirty.value) {
    window.$dialog?.warning({
      title: "有尚未保存的设置",
      content: "关闭后会丢弃本次输入；已保存的草稿和线上设置保留。",
      positiveText: "放弃修改",
      negativeText: "继续编辑",
      onPositiveClick: () => emit("update:show", false),
    });
  } else emit("update:show", false);
}
</script>
<template>
  <NModal
    :show="show"
    preset="card"
    :title="`主题设置 · ${themeKey === 'classic' ? '默认主题' : themeKey}`"
    style="width: 1480px; max-width: 96vw; height: 92vh"
    content-style="min-height:0;display:flex;flex-direction:column;overflow:hidden"
    :mask-closable="false"
    @update:show="!saving && close()"
  >
    <div class="theme-settings-bar">
      <NTag :type="dirty || state?.draft ? 'warning' : 'success'">{{
        dirty
          ? "未保存"
          : state?.draft
            ? "有未发布草稿"
            : state?.published
              ? "已发布设置"
              : "使用默认配置"
      }}</NTag>
      <NSelect
        v-if="versions.length"
        :value="state?.theme_revision"
        :options="versions"
        :disabled="dirty || saving"
        style="width: 180px"
        @update:value="load"
      />
      <NButton
        size="small"
        :disabled="saving || dirty"
        @click="load(state?.theme_revision)"
        >重新加载</NButton
      >
      <span class="theme-settings-note">{{
        state?.active
          ? "发布后影响当前商城"
          : "此主题尚未启用，发布设置不会切换商城主题"
      }}</span>
    </div>
    <NAlert v-if="error" type="error" class="mb-10px">{{ error }}</NAlert>
    <NSpin :show="loading" class="theme-settings-body">
      <NEmpty
        v-if="state && !state.schema"
        description="此主题未声明扩展设置，仍使用系统基础配置"
      />
      <div v-else-if="state?.schema" class="theme-settings-layout">
        <section class="theme-settings-editor">
          <nav class="theme-settings-groups" aria-label="主题设置分组">
            <button
              v-for="g in state.schema.groups"
              :key="g.id"
              :class="{ active: group === g.id }"
              @click="group = g.id"
            >
              {{ g.label }}
            </button>
          </nav>
          <div class="theme-settings-fields">
            <template v-for="f in fields" :key="f.key"
              ><div v-if="visible(f)" class="theme-settings-field">
                <label :for="f.key">{{ f.label }}</label>
                <NSwitch
                  v-if="f.type === 'switch'"
                  :id="f.key"
                  :aria-label="f.label"
                  :value="blocked(f) ? false : values[f.key]"
                  :disabled="!canWrite || blocked(f) || saving"
                  @update:value="values[f.key] = $event"
                />
                <NColorPicker
                  v-else-if="f.type === 'color'"
                  :id="f.key"
                  v-model:value="values[f.key]"
                  :show-alpha="false"
                  :modes="['hex']"
                  :disabled="!canWrite || saving"
                />
                <NSelect
                  v-else-if="f.type === 'select'"
                  :id="f.key"
                  v-model:value="values[f.key]"
                  :options="f.options"
                  :disabled="!canWrite || saving"
                />
                <NInputNumber
                  v-else-if="f.type === 'number'"
                  :id="f.key"
                  :input-props="{ 'aria-label': f.label }"
                  v-model:value="values[f.key]"
                  :min="f.min"
                  :max="f.max"
                  :disabled="!canWrite || saving"
                />
                <NSelect
                  v-else-if="f.type === 'product' || f.type === 'category'"
                  :id="f.key"
                  v-model:value="values[f.key]"
                  filterable
                  :remote="f.type === 'product'"
                  :options="choices"
                  :loading="choicesBusy"
                  :disabled="!canWrite || saving"
                  placeholder="输入名称搜索"
                  @focus="searchChoices(f.type)"
                  @search="searchChoices(f.type, $event)"
                />
                <MediaField
                  v-else-if="f.type === 'image' && canWrite && !saving"
                  :value="values[f.key] ? [values[f.key]] : []"
                  @update:value="values[f.key] = $event[0] || ''"
                />
                <NInput
                  v-else
                  :id="f.key"
                  v-model:value="values[f.key]"
                  :type="f.type === 'textarea' ? 'textarea' : 'text'"
                  :disabled="!canWrite || saving"
                />
                <p v-if="f.help">{{ f.help }}</p>
                <NAlert v-if="blocked(f)" type="info" :show-icon="false"
                  >系统已关闭此业务。请在系统业务设置中开启后调整入口展示。</NAlert
                >
              </div></template
            >
          </div>
        </section>
        <section class="theme-settings-preview">
          <div class="theme-preview-toolbar">
            <span>只读预览</span
            ><NSelect
              v-model:value="size"
              :options="[
                { label: '电脑', value: 'desktop' },
                { label: '平板', value: 'tablet' },
                { label: '手机', value: 'mobile' },
              ]"
              style="width: 110px"
            /><NButton
              :loading="previewBusy"
              :disabled="!canWrite"
              @click="preview"
              >{{ previewURL ? "刷新预览" : "打开预览" }}</NButton
            >
          </div>
          <div class="theme-preview-stage">
            <iframe
              v-if="previewURL"
              :src="previewURL"
              title="主题只读预览"
              :style="{ width: frameWidth }"
              sandbox="allow-scripts allow-same-origin"
              referrerpolicy="no-referrer"
            /><NEmpty
              v-else
              description="打开预览后，调整设置会自动更新画面；访客不受影响"
            />
          </div>
        </section>
      </div>
    </NSpin>
    <template #footer
      ><div class="theme-settings-footer">
        <NPopconfirm @positive-click="save('reset')"
          ><template #trigger
            ><NButton :disabled="!canWrite || !state?.schema || saving"
              >恢复默认</NButton
            ></template
          >将当前主题默认值存为草稿，发布前不影响访客。</NPopconfirm
        >
        <NPopconfirm @positive-click="save('rollback')"
          ><template #trigger
            ><NButton :disabled="!canWrite || !state?.can_rollback || saving"
              >恢复上一版</NButton
            ></template
          >恢复上一版已发布设置及其主题版本，立即生效。</NPopconfirm
        >
        <span class="theme-settings-spacer" />
        <NButton
          :disabled="!canWrite || !state?.schema"
          :loading="saving"
          @click="save('draft')"
          >保存草稿</NButton
        >
        <NButton
          type="primary"
          :disabled="!canWrite || !state?.schema"
          :loading="saving"
          @click="save('publish')"
          >发布设置</NButton
        >
      </div></template
    >
  </NModal>
</template>
<style scoped>
.theme-settings-bar,
.theme-settings-footer,
.theme-preview-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.theme-settings-bar {
  margin-bottom: 14px;
}
.theme-settings-note {
  font-size: 12px;
  color: #64748b;
}
.theme-settings-body {
  flex: 1;
  min-height: 0;
}
.theme-settings-body :deep(.n-spin-content) {
  height: 100%;
  min-height: 0;
}
.theme-settings-layout {
  height: 100%;
  display: grid;
  grid-template-columns: 360px minmax(0, 1fr);
  gap: 20px;
}
.theme-settings-editor {
  overflow: auto;
  border: 1px solid #e5e7eb;
  border-radius: 10px;
}
.theme-settings-groups {
  position: sticky;
  top: 0;
  z-index: 2;
  background: #fff;
  display: flex;
  overflow: auto;
  border-bottom: 1px solid #e5e7eb;
  padding: 6px;
  gap: 4px;
}
.theme-settings-groups button {
  min-height: 44px;
  padding: 8px 10px;
  white-space: nowrap;
  border: 0;
  background: transparent;
  border-radius: 6px;
  cursor: pointer;
}
.theme-settings-groups button.active {
  background: #eef2ff;
  color: #4f46e5;
}
.theme-settings-fields {
  padding: 18px;
}
.theme-settings-field {
  margin-bottom: 22px;
}
.theme-settings-field label {
  display: block;
  font-weight: 500;
  margin-bottom: 9px;
}
.theme-settings-field p {
  font-size: 12px;
  line-height: 1.6;
  color: #64748b;
  margin-top: 6px;
}
.theme-settings-preview {
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.theme-preview-toolbar {
  padding-bottom: 12px;
}
.theme-preview-stage {
  flex: 1;
  min-height: 0;
  background: #edf0f4;
  border-radius: 10px;
  overflow: auto;
  display: flex;
  justify-content: center;
  align-items: center;
}
.theme-preview-stage iframe {
  height: 100%;
  min-width: 320px;
  max-width: 100%;
  border: 0;
  background: #fff;
}
.theme-settings-spacer {
  flex: 1;
}
@media (max-width: 850px) {
  .theme-settings-layout {
    display: flex;
    flex-direction: column;
    overflow: auto;
  }
  .theme-settings-editor {
    overflow: visible;
    flex: none;
  }
  .theme-settings-preview {
    min-height: 520px;
    flex: none;
  }
  .theme-preview-stage {
    height: 460px;
    flex: none;
  }
  .theme-settings-footer {
    gap: 6px;
  }
  .theme-settings-footer :deep(button) {
    min-height: 44px;
  }
  .theme-settings-spacer {
    display: none;
  }
}
</style>
