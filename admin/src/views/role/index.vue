<template>
  <div class="min-h-500px">
    <NCard title="角色管理">
      <div class="mb-16px">
        <NButton type="primary" @click="showCreate = true">新增角色</NButton>
      </div>
      <NDataTable :columns="columns" :data="roles" :loading="loading" />
    </NCard>

    <!-- 新增角色 -->
    <NModal v-model:show="showCreate" preset="dialog" title="新增角色" style="width: 440px">
      <NForm :model="form" label-placement="left" label-width="70">
        <NFormItem label="名称" required>
          <NInput v-model:value="form.name" />
        </NFormItem>
        <NFormItem label="编码" required>
          <NInput v-model:value="form.code" placeholder="如：operator" />
        </NFormItem>
        <NFormItem label="描述">
          <NInput v-model:value="form.description" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showCreate = false">取消</NButton>
        <NButton type="primary" :loading="saving" @click="handleCreate">创建</NButton>
      </template>
    </NModal>

    <!-- 权限编辑 -->
    <NModal v-model:show="showPerms" preset="dialog" :title="`权限配置：${editingRole?.name || ''}`" style="width: 560px">
      <NTree
        v-model:checked-keys="checkedPerms"
        :data="permTree"
        checkable
        cascade
        selectable
        block-line
        key-field="key"
        label-field="label"
        children-field="children"
        :default-expand-all="true"
        class="max-h-400px overflow-auto"
      />
      <template #action>
        <NButton @click="showPerms = false">取消</NButton>
        <NButton type="primary" :loading="savingPerms" @click="handleSavePerms">保存</NButton>
      </template>
    </NModal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, h } from 'vue';
import { NButton, NTag, NSpace, NPopconfirm } from 'naive-ui';
import type { DataTableColumns } from 'naive-ui';
import {
  fetchRoles, fetchRole, createRole, updateRole, deleteRole,
  updateRolePermissions, fetchPermissionTree
} from '@/service/api';

defineOptions({ name: 'RoleManagement' });

const loading = ref(false);
const saving = ref(false);
const savingPerms = ref(false);
const showCreate = ref(false);
const showPerms = ref(false);
const roles = ref<any[]>([]);
const permTree = ref<any[]>([]);
const checkedPerms = ref<string[]>([]);
const editingRole = ref<any>(null);

const form = reactive({ name: '', code: '', description: '' });

const columns: DataTableColumns<any> = [
  { title: 'ID', key: 'id', width: 50 },
  { title: '名称', key: 'name', width: 120 },
  { title: '编码', key: 'code', width: 120 },
  { title: '描述', key: 'description', width: 160 },
  {
    title: '内置',
    key: 'is_builtin',
    width: 60,
    render: (row) => row.is_builtin ? h(NTag, { type: 'info', size: 'small' }, { default: () => '内置' }) : ''
  },
  {
    title: '权限数',
    key: 'perm_count',
    width: 70,
    render: (row) => (row.permissions || []).length
  },
  {
    title: '操作',
    key: 'actions',
    width: 180,
    render: (row) => h(NSpace, { size: 'small' }, {
      default: () => [
        h(NButton, { size: 'small', type: 'primary', onClick: () => handleEditPerms(row) }, { default: () => '权限' }),
        !row.is_builtin
          ? h(NPopconfirm, { onPositiveClick: () => handleDelete(row.id) }, {
              trigger: () => h(NButton, { size: 'small', type: 'error' }, { default: () => '删除' }),
              default: () => '确定删除该角色？'
            })
          : null
      ]
    })
  }
];

async function loadList() {
  loading.value = true;
  try {
    const { data, error } = await fetchRoles();
    if (!error && data) roles.value = (data as any).roles || [];
  } finally { loading.value = false; }
}

async function loadPermTree() {
  const { data, error } = await fetchPermissionTree();
  if (!error && data) {
    const groups = (data as any).groups || [];
    permTree.value = groups.map((g: any) => ({
      key: `group:${g.domain}`,
      label: g.label || g.domain,
      children: (g.perms || []).map((p: any) => ({
        key: p.code,
        label: `${p.desc}（${p.code}）`
      }))
    }));
  }
}

async function handleCreate() {
  if (!form.name || !form.code) return;
  saving.value = true;
  try {
    const { error } = await createRole(form);
    if (!error) {
      window.$message?.success('创建成功');
      showCreate.value = false;
      Object.assign(form, { name: '', code: '', description: '' });
      loadList();
    }
  } finally { saving.value = false; }
}

async function handleEditPerms(row: any) {
  editingRole.value = row;
  checkedPerms.value = (row.permissions || []).filter((p: string) => p !== '*');
  showPerms.value = true;
}

async function handleSavePerms() {
  if (!editingRole.value) return;
  savingPerms.value = true;
  try {
    const { error } = await updateRolePermissions(editingRole.value.id, checkedPerms.value);
    if (!error) {
      window.$message?.success('权限已更新');
      showPerms.value = false;
      loadList();
    }
  } finally { savingPerms.value = false; }
}

async function handleDelete(id: number) {
  const { error } = await deleteRole(id);
  if (!error) { window.$message?.success('删除成功'); loadList(); }
  else { window.$message?.error('删除失败（可能仍有员工挂载）'); }
}

onMounted(() => { loadList(); loadPermTree(); });
</script>
