<script setup lang="ts">
import { computed, ref, watch } from "vue";
import ChangePassword from "@/components/security/change-password.vue";
import AccountSecurity from "@/components/security/account-security.vue";
import { recoveryTicket } from "@/components/security/state";
import type { VNode } from "vue";
import { useAuthStore } from "@/store/modules/auth";
import { useRouterPush } from "@/hooks/common/router";
import { useSvgIcon } from "@/hooks/common/icon";
import { $t } from "@/locales";

defineOptions({
  name: "UserAvatar",
});

const authStore = useAuthStore();
const { toLogin } = useRouterPush();
const { SvgIconVNode } = useSvgIcon();

function loginOrRegister() {
  toLogin();
}

const showPassword = ref(false);
const showSecurity = ref(Boolean(recoveryTicket.value));
watch(recoveryTicket, (value) => {
  if (value) showSecurity.value = true;
});
type DropdownKey = "logout" | "security" | "password";

type DropdownOption =
  | {
      key: DropdownKey;
      label: string;
      icon?: () => VNode;
    }
  | {
      type: "divider";
      key: string;
    };

const options = computed(() => {
  const opts: DropdownOption[] = [
    {
      key: "security",
      label: "账号安全",
      icon: SvgIconVNode({ icon: "ph:shield-check", fontSize: 18 }),
    },
    {
      key: "password",
      label: "修改密码",
      icon: SvgIconVNode({ icon: "ph:key", fontSize: 18 }),
    },
    {
      label: $t("common.logout"),
      key: "logout",
      icon: SvgIconVNode({ icon: "ph:sign-out", fontSize: 18 }),
    },
  ];

  return opts;
});

function logout() {
  window.$dialog?.info({
    title: $t("common.tip"),
    content: $t("common.logoutConfirm"),
    positiveText: $t("common.confirm"),
    negativeText: $t("common.cancel"),
    onPositiveClick: async () => {
      await authStore.logout();
    },
  });
}

function handleDropdown(key: DropdownKey) {
  if (key === "logout") {
    logout();
  } else if (key === "password") {
    showPassword.value = true;
  } else {
    showSecurity.value = true;
  }
}
</script>

<template>
  <NButton v-if="!authStore.isLogin" quaternary @click="loginOrRegister">
    {{ $t("page.login.common.loginOrRegister") }}
  </NButton>
  <NDropdown v-else placement="bottom" trigger="click" :options="options" @select="handleDropdown">
    <div>
      <ButtonIcon>
        <SvgIcon icon="ph:user-circle" class="text-icon-large" />
        <span class="text-16px font-medium">{{ authStore.userInfo.userName }}</span>
      </ButtonIcon>
    </div>
  </NDropdown>
  <ChangePassword v-if="showPassword" @cancel="showPassword = false" @completed="showPassword = false" />
  <NModal
    v-model:show="showSecurity"
    preset="card"
    title="账号安全"
    style="width: 620px; max-width: calc(100vw - 24px)"
    ><AccountSecurity
  /></NModal>
</template>

<style scoped></style>
