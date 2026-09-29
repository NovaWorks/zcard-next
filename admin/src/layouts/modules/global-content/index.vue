<script setup lang="ts">
import { computed, onErrorCaptured, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { LAYOUT_SCROLL_EL_ID } from "@sa/materials";
import { useAppStore } from "@/store/modules/app";
import { useThemeStore } from "@/store/modules/theme";
import { useRouteStore } from "@/store/modules/route";
import { useTabStore } from "@/store/modules/tab";

defineOptions({
  name: "GlobalContent",
});

interface Props {
  /** Show padding for content */
  showPadding?: boolean;
}

withDefaults(defineProps<Props>(), {
  showPadding: true,
});

const appStore = useAppStore();
const themeStore = useThemeStore();
const routeStore = useRouteStore();
const tabStore = useTabStore();
const route = useRoute();
const renderFailed = ref(false);
onErrorCaptured((error) => {
  console.error(error);
  renderFailed.value = true;
  return false;
});
watch(() => route.fullPath, () => { renderFailed.value = false; });
function reloadContent() { renderFailed.value = false; }

const transitionName = computed(() => (themeStore.page.animate ? themeStore.page.animateMode : ""));

function resetScroll() {
  const el = document.querySelector(`#${LAYOUT_SCROLL_EL_ID}`);

  el?.scrollTo({ left: 0, top: 0 });
}
</script>

<template>
  <RouterView v-slot="{ Component, route }">
    <div v-if="renderFailed" role="alert" class="p-16px">
      <p>页面显示异常，请重试或切换其他菜单。</p>
      <button type="button" class="mt-12px border rounded px-16px py-8px" @click="reloadContent">重新加载内容</button>
    </div>
    <Transition
      v-else
      :name="transitionName"
      mode="out-in"
      @before-leave="appStore.setContentXScrollable(true)"
      @after-leave="resetScroll"
      @after-enter="appStore.setContentXScrollable(false)"
    >
      <KeepAlive :include="routeStore.cacheRoutes" :exclude="routeStore.excludeCacheRoutes">
        <component
          :is="Component"
          v-if="appStore.reloadFlag"
          :key="tabStore.getTabIdByRoute(route)"
          :class="{ 'p-16px': showPadding }"
          class="flex-grow bg-layout transition-300"
        />
      </KeepAlive>
    </Transition>
  </RouterView>
</template>

<style></style>
