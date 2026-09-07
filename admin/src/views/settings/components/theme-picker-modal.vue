<script setup lang="ts">
/**
 * 主题选择弹窗（WP 主题式交互）：卡片网格展示已安装主题——
 * 封面图/主题名/版本号/作者/描述；选中态高亮 + ✓ 角标；
 * 右上角支持本地上传 zip 安装（服务端解压校验后原子落盘）。
 */
import { ref, watch } from "vue";
import { NAlert, NButton, NModal, NSpin, NTag } from "naive-ui";
import { fetchTemplates, installTemplate, updateSettings } from "@/service/api";
import type { TemplateItem } from "@/service/api";

defineOptions({ name: "ThemePickerModal" });

const props = defineProps<{
  show: boolean;
  /** 当前已选主题 key（弹窗打开时高亮） */
  current?: string;
}>();
const emit = defineEmits<{
  (e: "update:show", v: boolean): void;
  (e: "select", key: string): void;
  (e: "installed"): void;
}>();

const loading = ref(false);
const installing = ref(false);
const activating = ref(false);
const templates = ref<TemplateItem[]>([]);
const selected = ref<string | null>(null);
const fileInput = ref<HTMLInputElement | null>(null);

async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchTemplates();
    if (!error && data) templates.value = data.templates || [];
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.show,
  (v) => {
    if (v) {
      selected.value = props.current || null;
      load();
    }
  },
);

function onFileChange(e: Event) {
  const input = e.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = ""; // 允许重复选择同一文件
  if (!file) return;
  if (!file.name.toLowerCase().endsWith(".zip")) {
    window.$message?.warning("请选择 zip 格式的主题包");
    return;
  }
  if (file.size > 20 * 1024 * 1024) {
    window.$message?.warning("主题包超过 20MB 上限");
    return;
  }
  installing.value = true;
  const reader = new FileReader();
  reader.onload = async () => {
    try {
      const base64 = String(reader.result).split(",")[1] || "";
      const { error } = await installTemplate(base64);
      if (!error) {
        window.$message?.success("主题安装成功，选择后点击「切换为默认」才会生效");
        emit("installed");
        await load();
      }
    } finally {
      installing.value = false;
    }
  };
  reader.onerror = () => { installing.value = false; window.$message?.error("读取主题包失败，请重新选择文件"); };
  reader.onabort = () => { installing.value = false; };
  reader.readAsDataURL(file);
}

async function confirm() {
  if (!selected.value || installing.value || activating.value) return;
  activating.value = true;
  try {
    const key = selected.value;
    const { error } = await updateSettings([
      { group: "template", key: "pc_template", value_json: JSON.stringify(key) },
    ]);
    if (error) return;
    emit("select", key);
    emit("update:show", false);
    window.$message?.success("默认主题已切换，刷新商城首页即可查看");
  } finally {
    activating.value = false;
  }
}
</script>

<template>
  <NModal
    :show="show"
    preset="card"
    title="商城主题"
    :closable="!installing && !activating"
    :mask-closable="!installing && !activating"
    :close-on-esc="!installing && !activating"
    style="width: 920px; max-width: 94vw"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <template #header-extra>
      <input
        ref="fileInput"
        type="file"
        accept=".zip"
        class="hidden"
        @change="onFileChange"
      />
      <NButton size="small" secondary type="primary" :loading="installing" :disabled="activating || installing" @click="fileInput?.click()">
        安装主题（zip）
      </NButton>
    </template>

    <NAlert type="info" :bordered="false" class="mb-16px">
      一个主题同时适配 PC 和手机。ZIP 上限 20MB，需包含 theme.json、编译后的 index.html 和静态资源；Vue 源码包不能直接安装。上传仅安装并显示在列表中；卡片显示最新安装版本，新主题或同名升级都需点击「切换为默认」才会生效。
    </NAlert>
    <NSpin :show="loading">
      <div v-if="templates.length" class="grid grid-cols-2 gap-14px sm:grid-cols-3">
        <div
          v-for="tp in templates"
          :key="tp.key"
          class="group cursor-pointer overflow-hidden rounded-10px border-2 transition-all"
          :class="
            selected === tp.key
              ? 'border-primary shadow-md'
              : 'border-gray-200 hover:border-gray-400 dark:border-gray-700'
          "
          @click="!activating && !installing && (selected = tp.key)"
        >
          <!-- 封面 -->
          <div class="relative h-120px bg-gray-100 dark:bg-gray-800">
            <img
              v-if="tp.preview"
              :src="tp.preview"
              class="h-full w-full object-cover"
              alt=""
              loading="lazy"
            />
            <div v-else class="flex h-full w-full items-center justify-center text-14px text-gray-500">
              {{ tp.name }}
            </div>
            <!-- 选中角标 -->
            <div
              v-if="selected === tp.key"
              class="absolute right-8px top-8px flex h-20px w-20px items-center justify-center rounded-full bg-primary text-12px text-white shadow"
            >
              ✓
            </div>
          </div>
          <!-- 信息区 -->
          <div class="px-10px py-8px">
            <div class="flex items-center justify-between gap-6px">
              <span class="truncate text-13px font-medium">{{ tp.name }}</span>
              <NTag v-if="tp.version" size="tiny" :bordered="false" type="info">v{{ tp.version }}</NTag>
            </div>
            <div v-if="tp.author" class="mt-4px truncate text-12px text-gray-400">作者：{{ tp.author }}</div>
            <div v-if="tp.desc" class="mt-2px truncate text-12px text-gray-400">{{ tp.desc }}</div>
          </div>
        </div>
      </div>
      <div v-else class="py-40px text-center text-13px text-gray-400">暂无可用主题，可点击右上角「安装主题」上传 zip 安装</div>
    </NSpin>

    <template #footer>
      <div class="flex justify-end gap-8px">
        <NButton :disabled="installing || activating" @click="emit('update:show', false)">关闭</NButton>
        <NButton type="primary" :disabled="!selected || installing || loading" :loading="activating" @click="confirm">切换为默认</NButton>
      </div>
    </template>
  </NModal>
</template>
