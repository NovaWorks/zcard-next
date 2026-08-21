<script setup lang="ts">
// 权限配置抽屉：按角色编辑权限点（员工页行内「权限」与角色管理抽屉共用）。
// 头部操作条：全选/反选 + 展开全部/折叠全部；超管（* 通配）打开时默认全勾，
// 保存后转为精确权限清单——取消勾选的功能即刻不可访问。
import { computed, ref, watch } from "vue";
import { NDrawer, NDrawerContent, NTree, NButton, NAlert, NSpace } from "naive-ui";
import { updateRolePermissions } from "@/service/api";

defineOptions({ name: "RolePermsDrawer" });

const props = defineProps<{
  show: boolean;
  role: any;
  permTree: any[];
}>();

const emit = defineEmits(["update:show", "saved"]);

const checked = ref<string[]>([]);
const expanded = ref<string[]>([]);
const saving = ref(false);

// 全量叶子权限码（勾选模型只存叶子码，父级勾选态由 cascade 自动推导）
const allCodes = computed<string[]>(() =>
  props.permTree.flatMap((g: any) => (g.children || []).map((c: any) => c.key as string)),
);
const groupKeys = computed<string[]>(() => props.permTree.map((g: any) => g.key as string));
const isWildcard = computed(() => (props.role?.permissions || []).includes("*"));
// cascade 勾选时 naive-ui 会把父级分组键一并写入 checked-keys——
// 保存与计数必须过滤出叶子权限码，否则分组键会作为权限点落库
const allCodeSet = computed<Set<string>>(() => new Set(allCodes.value));
const leafChecked = computed<string[]>(() => checked.value.filter((c) => allCodeSet.value.has(c)));
const allChecked = computed(() => leafChecked.value.length === allCodes.value.length);

watch(
  () => props.show,
  (v) => {
    if (!v) return;
    // 超管 * = 全部权限：打开即全勾（保存转为精确清单）；普通角色按已保存清单回显
    checked.value = isWildcard.value
      ? [...allCodes.value]
      : (props.role?.permissions || []).filter((p: string) => p !== "*");
    expanded.value = [...groupKeys.value]; // 默认展开全部
  },
);

function selectAll() {
  checked.value = [...allCodes.value];
}

function invertSelection() {
  checked.value = allCodes.value.filter((c) => !leafChecked.value.includes(c));
}

function expandAll() {
  expanded.value = [...groupKeys.value];
}

function collapseAll() {
  expanded.value = [];
}

async function handleSave() {
  if (!props.role) return;
  if (!leafChecked.value.length) {
    window.$message?.error("至少保留一项权限——清空将导致该角色（含你自己）失去后台访问能力");
    return;
  }
  saving.value = true;
  try {
    const { error } = await updateRolePermissions(props.role.id, leafChecked.value);
    if (!error) {
      // 成功提示由父组件 handlePermsSaved 统一反馈（含跳转场景），避免双弹窗
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
      <NAlert :type="isWildcard ? 'warning' : 'info'" :bordered="false" class="mb-12px">
        <template v-if="isWildcard">
          该角色当前为「全部权限（* 通配）」，保存后将转为<b>精确权限清单</b>——取消勾选的功能即刻不可访问。
        </template>
        <template v-else>
          正在修改角色「{{ role?.name || "" }}」的权限，对该角色下<b>所有员工</b>生效。
        </template>
      </NAlert>

      <!-- 头部操作条：批量勾选 + 树形态 -->
      <div class="mb-8px flex items-center gap-8px">
        <NSpace size="small">
          <NButton size="tiny" secondary @click="selectAll">全选</NButton>
          <NButton size="tiny" secondary @click="invertSelection">反选</NButton>
        </NSpace>
        <span class="text-12px text-gray-400">
          已选 {{ leafChecked.length }}/{{ allCodes.length }}
        </span>
        <NSpace size="small" class="ml-auto">
          <NButton size="tiny" quaternary @click="expandAll">展开全部</NButton>
          <NButton size="tiny" quaternary @click="collapseAll">折叠全部</NButton>
        </NSpace>
      </div>

      <NTree
        v-model:checked-keys="checked"
        v-model:expanded-keys="expanded"
        :data="permTree"
        checkable
        cascade
        selectable
        block-line
        key-field="key"
        label-field="label"
        children-field="children"
        class="max-h-540px overflow-auto"
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
            保存{{ allChecked ? "" : `（${leafChecked.length} 项）` }}
          </NButton>
        </div>
      </template>
    </NDrawerContent>
  </NDrawer>
</template>
