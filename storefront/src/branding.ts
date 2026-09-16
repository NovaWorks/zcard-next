import { computed, onMounted, reactive } from 'vue';
import { readThemeRuntime } from '../../packages/theme-sdk/src/index';
import { fetchSiteSeo } from './seo';

// Keep SSG and the initial hydration render identical: show a neutral placeholder.
// All brand locations switch together once the request-time identity is available.
const branding = reactive({ name: '', logo: '', ready: false });
let pending: Promise<void> | undefined;

async function loadBranding() {
  const initial = readThemeRuntime()?.branding;
  const site = initial && typeof initial.name === 'string' && typeof initial.logo === 'string'
    ? initial
    : await fetchSiteSeo();
  branding.name = site.name || '商店';
  branding.logo = site.logo || '';
  branding.ready = true;
}

export function useBranding() {
  onMounted(() => { pending ??= loadBranding(); });
  return {
    siteName: computed(() => branding.name),
    siteLogo: computed(() => branding.logo),
    brandReady: computed(() => branding.ready),
  };
}
