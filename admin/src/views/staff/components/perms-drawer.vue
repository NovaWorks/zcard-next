<script setup lang="ts">
// 权限配置抽屉：按角色编辑权限点（员工页行内「权限」与角色管理抽屉共用）。
import { ref, watch } from "vue";
import { NDrawer, NDrawerContent, NTree, NButton, NAlert } from "naive-ui";
import { updateRolePermissions } from "@/service/api";

defineOptions({ name: "RolePermsDrawer" });

const props = defineProps<{
  show: boolean;
  role: any;
  permTree: any[];
}>();

const emit = defineEmits(["update:show", "saved"]);

const checked = ref<string[]>([]);
const saving = ref(false);

watch(
  () => props.show,
  (v) => {
    if (v) {
      // 超管 * 通配不可勾选编辑（系统不变量，后端每次强制恢复）
      checked.value = (props.role?.permissions || []).filter((p: string) => p !== "*");
    }
  },
);

async function handleSave() {
  if (!props.role) return;
  saving.value = true;
  try {
    const { error } = await updateRolePermissions(props.role.id, checked.value);
    if (!error) {
      window.$message?.success("权限已更新（对该角色在线员工实时生效，菜单重新登录后刷新）");
      emit("saved");
      emit("update:show", false);
    }
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <NDrawer
    :show="show"
    :width="440"
    :auto-focus="false"
    :close-on-esc="false"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <NDrawerContent :title="`权限配置：${role?.name || ''}`" closable>
      <NAlert type="info" :bordered="false" class="mb-12px">
        正在修改角色「{{ role?.name || "" }}」的权限，对该角色下<b>所有员工</b>生效。
      </NAlert>
      <NTree
        v-model:checked-keys="checked"
        :data="permTree"
        checkable
        cascade
        selectable
        block-line
        key-field="key"
        label-field="label"
        children-field="children"
        :default-expand-all="true"
        class="max-h-560px overflow-auto"
      />
      <template #footer>
        <div class="flex justify-end gap-8px">
          <NButton @click="emit('update:show', false)">取消</NButton>
          <NButton
            v-auth="'authz:role_grant'"
            type="primary"
            :loading="saving"
            @click="handleSave"
          >
            保存
          </NButton>
        </div>
      </template>
    </NDrawerContent>
  </NDrawer>
</template>
