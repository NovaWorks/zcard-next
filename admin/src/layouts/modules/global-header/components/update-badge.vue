<script setup lang="ts">
// 更新可用徽标（方案 §10 静默检查面）：登录后/每 24h 一次静默检查（localStorage 节流），
// 有新版本 → header 红点徽标，点击直达设置页「系统更新」tab。静默容错——无权限/失败即隐藏。
import { ref, onMounted } from "vue";
import { useRouter } from "vue-router";
import { NBadge, NButton, NTooltip } from "naive-ui";
import { fetchUpdateStatus } from "@/service/api";
import { checkAuth } from "@/directives";

defineOptions({ name: "UpdateBadge" });

const router = useRouter();
const visible = ref(false);
const latest = ref("");

const LS_LAST_CHECK = "zcard.update.lastCheckAt";
const LS_DISMISSED = "zcard.update.dismissedVer";
const THROTTLE_MS = 24 * 60 * 60 * 1000;

onMounted(async () => {
  if (!checkAuth("system:update")) return;
  const last = Number(localStorage.getItem(LS_LAST_CHECK) || 0);
  if (Date.now() - last < THROTTLE_MS) return;
  localStorage.setItem(LS_LAST_CHECK, String(Date.now()));
  try {
    const st: any = await fetchUpdateStatus();
    if (!st?.has_update || !st?.latest_version) return;
    if (localStorage.getItem(LS_DISMISSED) === st.latest_version) return;
    latest.value = st.latest_version;
    visible.value = true;
  } catch {
    // 静默检查失败不打扰（无权限/网络抖动）
  }
});

function goUpdate() {
  visible.value = false;
  router.push({ path: "/settings", query: { tab: "update" } });
}
</script>

<template>
  <NTooltip v-if="visible" placement="bottom">
    <template #trigger>
      <NBadge dot type="success" processing>
        <NButton size="tiny" quaternary circle aria-label="发现新版本" @click="goUpdate">
          <template #icon>
            <icon-mdi-cloud-upload-outline />
          </template>
        </NButton>
      </NBadge>
    </template>
    发现新版本 {{ latest }}，点击前往更新
  </NTooltip>
</template>
