<script setup lang="ts">
/**
 * 自定义控件管理（P1-01 M1b 前端面）：
 * 下单收集字段（text|password|select|number|checkbox|radio）+ 必填 + 选项。
 */
import { ref, reactive, computed, watch, h } from "vue";
import { NButton, NTag, NSpace, NPopconfirm } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchControls, createControl, updateControl, deleteControl } from "@/service/api";
import { checkAuth } from "@/directives";

const props = defineProps<{ productId: number }>();

const loading = ref(false);
const saving = ref(false);
const controls = ref<any[]>([]);
const showForm = ref(false);
const editingId = ref(0);

const formData = reactive({
  name: "",
  type: "text",
  required: false,
  options_text: "",
  sort: 0,
});

const typeOptions = [
  { label: "文本", value: "text" },
  { label: "密码", value: "password" },
  { label: "下拉选择", value: "select" },
  { label: "数字", value: "number" },
  { label: "多选", value: "checkbox" },
  { label: "单选", value: "radio" },
];

const needOptions = computed(() => ["select", "checkbox", "radio"].includes(formData.type));

const columns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 56 },
  { title: "字段名", key: "name", minWidth: 120 },
  {
    title: "类型",
    key: "type",
    width: 90,
    render: (row) =>
      h(
        NTag,
        { size: "small", bordered: false },
        { default: () => typeOptions.find((t) => t.value === row.type)?.label || row.type },
      ),
  },
  {
    title: "必填",
    key: "required",
    width: 70,
    render: (row) =>
      h(
        NTag,
        { size: "small", type: row.required ? "error" : "default" },
        { default: () => (row.required ? "必填" : "选填") },
      ),
  },
  {
    title: "选项",
    key: "options",
    minWidth: 180,
    render: (row) =>
      row.options?.length
        ? h(
            NSpace,
            { size: "small" },
            {
              default: () =>
                row.options.map((o: string) =>
                  h(NTag, { size: "tiny", bordered: false }, { default: () => o }),
                ),
            },
          )
        : "-",
  },
  { title: "排序", key: "sort", width: 60 },
  {
    title: "操作",
    key: "actions",
    width: 140,
    render: (row) =>
      h(
        NSpace,
        { size: "small" },
        {
          default: () => [
            checkAuth("catalog:control_write")
              ? h(
                  NButton,
                  { size: "small", onClick: () => handleEdit(row) },
                  { default: () => "编辑" },
                )
              : null,
            checkAuth("catalog:control_delete")
              ? h(
                  NPopconfirm,
                  { onPositiveClick: () => handleDelete(row.id) },
                  {
                    trigger: () =>
                      h(NButton, { size: "small", type: "error" }, { default: () => "删除" }),
                    default: () => "确定删除该控件？",
                  },
                )
              : null,
          ],
        },
      ),
  },
];

async function load() {
  if (!props.productId) return;
  loading.value = true;
  try {
    const { data, error } = await fetchControls(props.productId);
    if (!error && data) controls.value = (data as any).controls || [];
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.productId,
  (v) => {
    if (v) {
      load();
      resetForm();
    }
  },
  { immediate: true },
);

function resetForm() {
  editingId.value = 0;
  Object.assign(formData, { name: "", type: "text", required: false, options_text: "", sort: 0 });
  showForm.value = false;
}

function handleEdit(row: any) {
  editingId.value = row.id;
  Object.assign(formData, {
    name: row.name,
    type: row.type,
    required: row.required,
    options_text: (row.options || []).join(","),
    sort: row.sort || 0,
  });
  showForm.value = true;
}

async function handleSave() {
  if (!formData.name) return;
  if (needOptions.value && !formData.options_text.trim()) {
    window.$message?.error("该类型必须提供选项（逗号分隔）");
    return;
  }
  saving.value = true;
  try {
    const payload = {
      name: formData.name,
      type: formData.type,
      required: formData.required,
      options: needOptions.value
        ? formData.options_text
            .split(/[,，]/)
            .map((s) => s.trim())
            .filter(Boolean)
        : [],
      sort: formData.sort || 0,
    };
    const { error } = editingId.value
      ? await updateControl(editingId.value, payload)
      : await createControl(props.productId, payload);
    if (!error) {
      window.$message?.success(editingId.value ? "控件已更新" : "控件已创建");
      resetForm();
      load();
    }
  } finally {
    saving.value = false;
  }
}

async function handleDelete(id: number) {
  const { error } = await deleteControl(id);
  if (!error) {
    window.$message?.success("已删除");
    load();
  }
}
</script>

<template>
  <div>
    <div class="mb-12px">
      <NButton v-auth="'catalog:control_write'" size="small" type="primary" @click="showForm = !showForm">
        {{ showForm ? "收起" : "新增控件" }}
      </NButton>
    </div>

    <!-- 控件编辑（大厂表单风格：顶部标签 + 弹性行，杜绝截断） -->
    <NCard
      v-if="showForm"
      size="small"
      class="mb-12px"
      :title="editingId ? '编辑控件' : '新增控件'"
    >
      <NForm :model="formData" label-placement="top" class="mb-4px">
        <div class="flex flex-wrap items-end gap-x-16px gap-y-4px">
          <NFormItem label="字段名（必填）" class="min-w-180px flex-1">
            <NInput v-model:value="formData.name" placeholder="如：充值账号" />
          </NFormItem>
          <NFormItem label="类型" class="w-150px">
            <!-- 菜单宽度不随输入框（consistent-menu-width=false）——选项文字完整 -->
            <NSelect
              v-model:value="formData.type"
              :options="typeOptions"
              :consistent-menu-width="false"
            />
          </NFormItem>
          <NFormItem label="必填" class="w-70px">
            <NSwitch v-model:value="formData.required" />
          </NFormItem>
          <NFormItem label="排序" class="w-90px">
            <NInputNumber v-model:value="formData.sort" :precision="0" class="w-full" />
          </NFormItem>
        </div>
        <NFormItem v-if="needOptions" label="选项（逗号分隔）">
          <NInput v-model:value="formData.options_text" placeholder="如：微信,QQ,邮箱" />
        </NFormItem>
      </NForm>
      <div class="flex items-center justify-between">
        <span class="text-12px text-gray-400">控件在下单表单渲染，答案随订单落库</span>
        <NSpace>
          <NButton v-if="editingId" size="small" @click="resetForm">取消编辑</NButton>
          <NButton v-auth="'catalog:control_write'" type="primary" size="small" :loading="saving" @click="handleSave">
            {{ editingId ? "更新控件" : "创建控件" }}
          </NButton>
        </NSpace>
      </div>
    </NCard>

    <NDataTable
      :columns="columns"
      :data="controls"
      :loading="loading"
      :bordered="false"
      size="small"
    />
    <NEmpty
      v-if="!controls.length && !loading"
      size="small"
      class="mt-16px"
      description="暂无控件——如「充值账号」「选择联系方式」等下单收集字段"
    />
  </div>
</template>
