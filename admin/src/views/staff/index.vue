<template>
  <div class="min-h-500px">
    <NCard title="员工管理">
      <div class="mb-16px">
        <NButton type="primary" @click="showCreate = true">新增员工</NButton>
      </div>
      <NDataTable :columns="columns" :data="admins" :loading="loading" />
    </NCard>

    <NModal v-model:show="showCreate" preset="dialog" title="新增员工" style="width: 480px">
      <NForm :model="form" label-placement="left" label-width="80">
        <NFormItem label="用户名" required>
          <NInput v-model:value="form.username" />
        </NFormItem>
        <NFormItem label="密码" required>
          <NInput v-model:value="form.password" type="password" show-password-on="click" placeholder="至少 8 位" />
        </NFormItem>
        <NFormItem label="昵称">
          <NInput v-model:value="form.nickname" />
        </NFormItem>
        <NFormItem label="角色" required>
          <NSelect v-model:value="form.role_id" :options="roleOptions" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showCreate = false">取消</NButton>
        <NButton type="primary" :loading="saving" @click="handleCreate">创建</NButton>
      </template>
    </NModal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, h } from 'vue';
import { NButton, NTag, NSwitch } from 'naive-ui';
import type { DataTableColumns } from 'naive-ui';
import { fetchAdmins, createAdmin, toggleAdmin, fetchRoles } from '@/service/api';

defineOptions({ name: 'StaffManagement' });

const loading = ref(false);
const saving = ref(false);
const showCreate = ref(false);
const admins = ref<any[]>([]);
const roles = ref<any[]>([]);

const form = reactive({ username: '', password: '', nickname: '', role_id: null as number | null });

const roleOptions = computed(() => roles.value.map(r => ({ label: r.name, value: r.id })));

const columns: DataTableColumns<any> = [
  { title: 'ID', key: 'id', width: 50 },
  { title: '用户名', key: 'username', width: 120 },
  { title: '昵称', key: 'nickname', width: 120 },
  { title: '角色', key: 'role_name', width: 100 },
  {
    title: 'TOTP',
    key: 'totp_enabled',
    width: 70,
    render: (row) => row.totp_enabled ? '✓' : '-'
  },
  {
    title: '启用',
    key: 'enabled',
    width: 80,
    render: (row) => h(NSwitch, {
      value: row.enabled,
      onUpdateValue: (val: boolean) => handleToggle(row, val)
    })
  }
];

async function loadList() {
  loading.value = true;
  try {
    const [adminRes, roleRes] = await Promise.all([fetchAdmins(), fetchRoles()]);
    if (!adminRes.error && adminRes.data) {
      const list = (adminRes.data as any).admins || [];
      const roleMap = new Map(roles.value.map(r => [r.id, r.name]));
      admins.value = list.map((a: any) => ({ ...a, role_name: roleMap.get(a.role_id) || '-' }));
    }
    if (!roleRes.error && roleRes.data) {
      roles.value = (roleRes.data as any).roles || [];
      // 重新映射角色名
      const roleMap = new Map(roles.value.map(r => [r.id, r.name]));
      admins.value = admins.value.map((a: any) => ({ ...a, role_name: roleMap.get(a.role_id) || '-' }));
    }
  } finally { loading.value = false; }
}

async function handleCreate() {
  if (!form.username || !form.password || !form.role_id) return;
  saving.value = true;
  try {
    const { error } = await createAdmin(form);
    if (!error) {
      window.$message?.success('创建成功');
      showCreate.value = false;
      Object.assign(form, { username: '', password: '', nickname: '', role_id: null });
      loadList();
    }
  } finally { saving.value = false; }
}

async function handleToggle(row: any, val: boolean) {
  const { error } = await toggleAdmin(row.id, val);
  if (!error) { row.enabled = val; window.$message?.success(val ? '已启用' : '已停用'); }
}

onMounted(loadList);
</script>
