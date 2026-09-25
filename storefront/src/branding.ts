import { computed, onMounted, reactive, watch } from 'vue';
import { readThemeRuntime } from '../../packages/theme-sdk/src/index';
import { publicConfig } from './config';
import { fetchSiteSeo } from './seo';

// Use the request-time public identity before mounting; every brand location shares it.
const initialBrand = readThemeRuntime()?.branding;
const branding = reactive({ name: initialBrand?.name || '', logo: initialBrand?.logo || '', ready: !!initialBrand });
watch(publicConfig, config => {
  if (!config) return;
  const read = (key: string) => { try { return JSON.parse(config.entries.find(e => e.key === key)?.value_json || 'null'); } catch { return null; } };
  const name = read('site.name'), logo = read('site.logo');
  if (typeof name === 'string' && typeof logo === 'string') {
    branding.name = name || '商店'; branding.logo = logo; branding.ready = true;
  }
}, { immediate: true });
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
  onMounted(() => { if (!branding.ready) pending ??= loadBranding(); });
  return {
    siteName: computed(() => branding.name),
    siteLogo: computed(() => branding.logo),
    brandReady: computed(() => branding.ready),
  };
}
