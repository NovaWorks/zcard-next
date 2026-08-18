<script setup lang="ts">
// 员工与权限一体化管理（大厂控制台模式）：员工为主轴、角色内聚为抽屉——
// 行内「权限」直达该员工角色的权限配置，无需独立角色页。
import { ref, reactive, computed, onMounted, h } from "vue";
import {
  NButton,
  NSwitch,
  NTag,
  NDropdown,
  NSpace,
} from "naive-ui";
import type { DataTableColumns, DropdownOption } from "naive-ui";
import {
  fetchAdmins,
  createAdmin,
  updateAdmin,
  toggleAdmin,
  deleteAdmin,
  resetAdminPassword,
  resetAdminTOTP,
  fetchRoles,
  fetchPermissionTree,
} from "@/service/api";
import { checkAuth } from "@/directives";
import RoleManageDrawer from "./components/role-manage-drawer.vue";
import PermsDrawer from "./components/perms-drawer.vue";

defineOptions({ name: "StaffManagement" });

const loading = ref(false);
const saving = ref(false);
const showCreate = ref(false);
const showEdit = ref(false);
const admins = ref<any[]>([]);
const roles = ref<any[]>([]);
const permTree = ref<any[]>([]);
const editing = ref<any>(null);

// 角色管理抽屉 / 权限配置抽屉
const showRoleDrawer = ref(false);
const showPermsDrawer = ref(false);
const permsRole = ref<any>(null);

const form = reactive({ username: "", password: "", nickname: "", role_id: null as number | null });
const editForm = reactive({ nickname: "", role_id: null as number | null });

// 重置密码
const showResetPwd = ref(false);
const resettingPwd = ref(false);
const resetPwdTarget = ref<any>(null);
const resetPwdForm = reactive({ password: "" });

const roleMap = computed(() => new Map(roles.value.map((r) => [r.id, r])));
const roleOptions = computed(() => roles.value.map((r) => ({ label: r.name, value: r.id })));
const memberCount = computed(() => {
  const counts = new Map<number, number>();
  admins.value.forEach((a) => counts.set(a.role_id, (counts.get(a.role_id) || 0) + 1));
  return counts;
});

// 是否内置超级管理员（系统账号：不可删除，列表打「系统」标）
function isSuper(row: any) {
  return roleMap.value.get(row.role_id)?.code === "super_admin";
}

const columns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 50 },
  {
    title: "用户名",
    key: "username",
    width: 150,
    render: (row) =>
      isSuper(row)
        ? h(
            NSpace,
            { size: 6, wrap: false, align: "center" },
            {
              default: () => [
                h("span", {}, row.username),
                h(NTag, { type: "info", size: "small", bordered: false }, { default: () => "系统" }),
              ],
            },
          )
        : row.username,
  },
  { title: "昵称", key: "nickname", width: 110 },
  {
    title: "角色",
    key: "role",
    width: 110,
    render: (row) => {
      const role = roleMap.value.get(row.role_id);
      return role
        ? h(
            NTag,
            { size: "small", type: isSuper(row) ? "primary" : "default", bordered: !isSuper(row) },
            { default: () => role.name },
          )
        : "-";
    },
  },
  {
    title: "TOTP",
    key: "totp_enabled",
    width: 64,
    render: (row) => (row.totp_enabled ? "✓" : "-"),
  },
  {
    title: "启用",
    key: "enabled",
    width: 72,
    render: (row) =>
      checkAuth("identity:admin_toggle")
        ? h(NSwitch, {
            value: row.enabled,
            onUpdateValue: (val: boolean) => handleToggle(row, val),
          })
        : row.enabled
          ? "✓"
          : "-",
  },
  {
    title: "操作",
    key: "actions",
    width: 220,
    render: (row) =>
      h(
        NSpace,
        { size: 4, wrap: false },
        {
          default: () => [
            checkAuth("authz:role_grant")
              ? h(
                  NButton,
                  { size: "small", type: "primary", secondary: true, onClick: () => openPerms(row) },
                  { default: () => "权限" },
                )
              : null,
            checkAuth("identity:admin_write")
              ? h(
                  NButton,
                  { size: "small", onClick: () => openEdit(row) },
                  { default: () => "编辑" },
                )
              : null,
            moreMenu(row),
          ],
        },
      ),
  },
];

// 次要操作收敛为「更多」下拉（大厂表格惯例：主操作外露，次操作收进 ⋯）
function moreMenu(row: any) {
  const options: DropdownOption[] = [];
  if (checkAuth("identity:admin_reset_pwd")) {
    options.push({ label: "重置密码", key: "reset-pwd" });
  }
  if (row.totp_enabled && checkAuth("identity:admin_totp_reset")) {
    options.push({ label: "解绑 TOTP", key: "totp-reset" });
  }
  if (!isSuper(row) && checkAuth("identity:admin_delete")) {
    options.push({ label: "删除员工", key: "delete" });
  }
  if (!options.length) return null;
  return h(
    NDropdown,
    {
      options,
      trigger: "click",
      onSelect: (key: string | number) => handleMore(key, row),
    },
    { default: () => h(NButton, { size: "small", quaternary: true }, { default: () => "⋯" }) },
  );
}

function handleMore(key: string | number, row: any) {
  switch (key) {
    case "reset-pwd":
      openResetPwd(row);
      break;
    case "totp-reset":
      handleResetTOTP(row);
      break;
    case "delete":
      handleDelete(row);
      break;
  }
}

async function loadList() {
  loading.value = true;
  try {
    // 先角色后员工：roleMap/super 判定依赖 roles 已加载
    const roleRes = await fetchRoles();
    if (!roleRes.error && roleRes.data) {
      roles.value = (roleRes.data as any).roles || [];
    }
    const adminRes = await fetchAdmins();
    if (!adminRes.error && adminRes.data) {
      admins.value = (adminRes.data as any).admins || [];
    }
  } finally {
    loading.value = false;
  }
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
        label: `${p.desc}（${p.code}）`,
      })),
    }));
  }
}

// ── 权限抽屉（行内入口：编辑该员工所属角色的权限）──────
function openPerms(row: any) {
  const role = roleMap.value.get(row.role_id);
  if (!role) return;
  permsRole.value = role;
  showPermsDrawer.value = true;
}

// ── 员工 CRUD ──────────────────────────────────
function openEdit(row: any) {
  editing.value = row;
  editForm.nickname = row.nickname || "";
  editForm.role_id = row.role_id;
  showEdit.value = true;
}

async function handleEdit() {
  if (!editing.value || !editForm.role_id) return;
  saving.value = true;
  try {
    const { error } = await updateAdmin(editing.value.id, {
      nickname: editForm.nickname,
      role_id: editForm.role_id,
    });
    if (!error) {
      window.$message?.success("已保存（角色变更即时生效）");
      showEdit.value = false;
      loadList();
    }
  } finally {
    saving.value = false;
  }
}

async function handleCreate() {
  if (!form.username || !form.password || !form.role_id) return;
  saving.value = true;
  try {
    const { error } = await createAdmin({ ...form, role_id: form.role_id as number });
    if (!error) {
      window.$message?.success("创建成功");
      showCreate.value = false;
      Object.assign(form, { username: "", password: "", nickname: "", role_id: null });
      loadList();
    }
  } finally {
    saving.value = false;
  }
}

async function handleToggle(row: any, val: boolean) {
  const { error } = await toggleAdmin(row.id, val);
  if (!error) {
    row.enabled = val;
    window.$message?.success(val ? "已启用" : "已停用（其在线会话即时失效）");
  } else {
    loadList(); // 末位超管等后端拒绝场景：回读真实状态
  }
}

async function handleDelete(row: any) {
  const { error } = await deleteAdmin(row.id);
  if (!error) {
    window.$message?.success(`已删除员工「${row.username}」（其全部后台会话已清除）`);
    loadList();
  }
}

function openResetPwd(row: any) {
  resetPwdTarget.value = row;
  resetPwdForm.password = "";
  showResetPwd.value = true;
}

async function handleResetPwd() {
  if (!resetPwdTarget.value || resetPwdForm.password.length < 8) return;
  resettingPwd.value = true;
  try {
    const { error } = await resetAdminPassword(resetPwdTarget.value.id, resetPwdForm.password);
    if (!error) {
      window.$message?.success("密码已重置（该员工全部后台会话已登出）");
      showResetPwd.value = false;
    }
  } finally {
    resettingPwd.value = false;
  }
}

async function handleResetTOTP(row: any) {
  const { error } = await resetAdminTOTP(row.id);
  if (!error) {
    window.$message?.success("已解绑 TOTP（该员工下次登录可重新绑定）");
    loadList();
  }
}

onMounted(() => {
  loadList();
  loadPermTree();
});
</script>

<template>
  <div class="min-h-500px">
    <NCard title="员工管理">
      <!-- 顶栏：主操作「新增员工」+ 角色入口（权限表现层收敛于此页） -->
      <div class="mb-16px flex items-center gap-12px">
        <NButton v-auth="'identity:admin_write'" type="primary" @click="showCreate = true">
          新增员工
        </NButton>
        <NButton v-auth="'authz:role_read'" @click="showRoleDrawer = true"> 角色管理 </NButton>
        <span class="ml-auto text-12px text-gray-400">
          行内「权限」= 修改该员工所属角色的权限点（对同角色所有人生效）
        </span>
      </div>
      <NDataTable :columns="columns" :data="admins" :loading="loading" :row-key="(r: any) => r.id" />
    </NCard>

    <!-- 新增员工 -->
    <NModal v-model:show="showCreate" preset="dialog" title="新增员工" style="width: 480px">
      <NForm :model="form" label-placement="left" label-width="80">
        <NFormItem label="用户名" required>
          <NInput v-model:value="form.username" />
        </NFormItem>
        <NFormItem label="密码" required>
          <NInput
            v-model:value="form.password"
            type="password"
            show-password-on="click"
            placeholder="至少 8 位"
          />
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

    <!-- 编辑员工（昵称/角色；改角色即时生效——后端中间件按库中当前 RoleID 判权） -->
    <NModal
      v-model:show="showEdit"
      preset="dialog"
      :title="`编辑员工：${editing?.username || ''}`"
      style="width: 440px"
    >
      <NForm :model="editForm" label-placement="left" label-width="80">
        <NFormItem label="昵称">
          <NInput v-model:value="editForm.nickname" />
        </NFormItem>
        <NFormItem label="角色" required>
          <NSelect v-model:value="editForm.role_id" :options="roleOptions" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showEdit = false">取消</NButton>
        <NButton type="primary" :loading="saving" @click="handleEdit">保存</NButton>
      </template>
    </NModal>

    <!-- 重置密码（高危：吊销该员工全部后台会话强制重登） -->
    <NModal
      v-model:show="showResetPwd"
      preset="dialog"
      :title="`重置密码：${resetPwdTarget?.username || ''}`"
      style="width: 440px"
    >
      <NForm :model="resetPwdForm" label-placement="left" label-width="80">
        <NFormItem label="新密码" required>
          <NInput
            v-model:value="resetPwdForm.password"
            type="password"
            show-password-on="click"
            placeholder="至少 8 位；重置后该员工全部后台会话立即登出"
          />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showResetPwd = false">取消</NButton>
        <NButton
          v-auth="'identity:admin_reset_pwd'"
          type="primary"
          :loading="resettingPwd"
          :disabled="resetPwdForm.password.length < 8"
          @click="handleResetPwd"
        >
          重置
        </NButton>
      </template>
    </NModal>

    <!-- 角色管理抽屉（新建/编辑/删除角色 + 权限入口） -->
    <RoleManageDrawer
      v-model:show="showRoleDrawer"
      :roles="roles"
      :member-count="memberCount"
      @refresh="loadList"
      @open-perms="(r: any) => ((permsRole = r), (showPermsDrawer = true))"
    />

    <!-- 权限配置抽屉（行内「权限」与角色抽屉共用） -->
    <PermsDrawer
      v-model:show="showPermsDrawer"
      :role="permsRole"
      :perm-tree="permTree"
      @saved="loadList"
    />
  </div>
</template>
