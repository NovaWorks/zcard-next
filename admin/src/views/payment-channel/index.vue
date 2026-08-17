<script setup lang="ts">
import { ref, reactive, onMounted, h } from "vue";
import { NButton, NPopconfirm, NSwitch } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchChannels, createChannel, updateChannel, deleteChannel } from "@/service/api";

defineOptions({ name: "PaymentChannelManagement" });

const loading = ref(false);
const saving = ref(false);
const showCreate = ref(false);
const channels = ref<any[]>([]);

const form = reactive({
  name: "",
  code: "",
  driver: "wallet",
  config_json: "{}",
  enabled: true,
});

const driverOptions = [
  { label: "余额支付", value: "wallet" },
  { label: "支付宝", value: "alipay" },
  { label: "微信支付", value: "wechat" },
  { label: "易支付", value: "epay" },
];

const columns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 50 },
  { title: "名称", key: "name", width: 140 },
  { title: "编码", key: "code", width: 100 },
  { title: "驱动", key: "driver", width: 100 },
  { title: "凭据", key: "config_json", width: 80, render: () => "****" },
  {
    title: "状态",
    key: "enabled",
    width: 80,
    render: (row) =>
      h(NSwitch, {
        value: row.enabled,
        onUpdateValue: (val: boolean) => handleToggle(row, val),
      }),
  },
  {
    title: "操作",
    key: "actions",
    width: 80,
    render: (row) =>
      h(
        NPopconfirm,
        { onPositiveClick: () => handleDelete(row.id) },
        {
          trigger: () => h(NButton, { size: "small", type: "error" }, { default: () => "删除" }),
          default: () => "确定删除该渠道？",
        },
      ),
  },
];

async function loadList() {
  loading.value = true;
  try {
    const { data, error } = await fetchChannels();
    if (!error && data) channels.value = (data as any).channels || [];
  } finally {
    loading.value = false;
  }
}

async function handleCreate() {
  if (!form.name || !form.code) return;
  saving.value = true;
  try {
    const { error } = await createChannel(form);
    if (!error) {
      window.$message?.success("创建成功");
      showCreate.value = false;
      Object.assign(form, {
        name: "",
        code: "",
        driver: "wallet",
        config_json: "{}",
        enabled: true,
      });
      loadList();
    }
  } finally {
    saving.value = false;
  }
}

async function handleToggle(row: any, val: boolean) {
  const { error } = await updateChannel(row.id, { enabled: val });
  if (!error) {
    row.enabled = val;
    window.$message?.success(val ? "已启用" : "已停用");
  }
}

async function handleDelete(id: number) {
  const { error } = await deleteChannel(id);
  if (!error) {
    window.$message?.success("删除成功");
    loadList();
  }
}

onMounted(loadList);
</script>

<template>
  <div class="min-h-500px">
    <NCard title="支付渠道">
      <div class="mb-16px">
        <NButton type="primary" @click="showCreate = true">新增渠道</NButton>
      </div>
      <NDataTable :columns="columns" :data="channels" :loading="loading" />
    </NCard>

    <NModal v-model:show="showCreate" preset="dialog" title="新增支付渠道" style="width: 560px">
      <NForm :model="form" label-placement="left" label-width="90">
        <NFormItem label="名称" required>
          <NInput v-model:value="form.name" placeholder="如：支付宝当面付" />
        </NFormItem>
        <NFormItem label="编码" required>
          <NInput v-model:value="form.code" placeholder="如：alipay" />
        </NFormItem>
        <NFormItem label="驱动" required>
          <NSelect v-model:value="form.driver" :options="driverOptions" />
        </NFormItem>
        <NFormItem label="凭据 JSON" required>
          <NInput
            v-model:value="form.config_json"
            type="textarea"
            :rows="5"
            placeholder='{"app_id":"...","private_key":"..."}'
          />
        </NFormItem>
        <NFormItem label="启用">
          <NSwitch v-model:value="form.enabled" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showCreate = false">取消</NButton>
        <NButton type="primary" :loading="saving" @click="handleCreate">创建</NButton>
      </template>
    </NModal>
  </div>
</template>
