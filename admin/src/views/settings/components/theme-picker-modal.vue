<script setup lang="ts">
/**
 * 主题选择弹窗（WP 主题式交互）：卡片网格展示已安装主题——
 * 封面图/主题名/版本号/作者/描述；选中态高亮 + ✓ 角标；
 * 右上角支持本地上传 zip 安装（服务端解压校验后原子落盘）。
 */
import { ref, watch } from "vue";
import { NButton, NModal, NSpin, NTag } from "naive-ui";
import { fetchTemplates, installTemplate } from "@/service/api";
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
}>();

const loading = ref(false);
const installing = ref(false);
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
        window.$message?.success("主题安装成功");
        await load();
      }
    } finally {
      installing.value = false;
    }
  };
  reader.readAsDataURL(file);
}

function confirm() {
  if (!selected.value) return;
  emit("select", selected.value);
  emit("update:show", false);
}
</script>

<template>
  <NModal
    :show="show"
    preset="card"
    title="选择主题"
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
      <NButton size="small" secondary type="primary" :loading="installing" @click="fileInput?.click()">
        安装主题（zip）
      </NButton>
    </template>

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
          @click="selected = tp.key"
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
        <NButton @click="emit('update:show', false)">取消</NButton>
        <NButton type="primary" :disabled="!selected" @click="confirm">确定</NButton>
      </div>
    </template>
  </NModal>
</template>
