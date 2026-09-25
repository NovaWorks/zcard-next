import { shallowRef } from 'vue';
import { mergeThemeConfig, readThemeRuntime, type ConfigEntry } from '../../packages/theme-sdk/src/index';
import { readJSON } from './api/read';

export type PublicConfig = { entries: ConfigEntry[] };
function validConfig(value: unknown): value is PublicConfig {
  if (!value || !Array.isArray((value as PublicConfig).entries) || !(value as PublicConfig).entries.length) return false;
  return (value as PublicConfig).entries.every(e => {
    if (typeof e?.key !== 'string' || typeof e.value_json !== 'string') return false;
    try { JSON.parse(e.value_json); return true; } catch { return false; }
  });
}
const seed = readThemeRuntime()?.public_config;
export const publicConfig = shallowRef<PublicConfig | null>(validConfig(seed) ? { entries: mergeThemeConfig(seed.entries) } : null);
let loadedAt = publicConfig.value ? Date.now() : 0;
let pending: Promise<PublicConfig> | undefined;

// Request-local HTML is the initial authority. No persistent business/config cache:
// a reload immediately sees new publications and never inherits another site's state.
export async function loadPublicConfig(force = false): Promise<PublicConfig> {
  if (!force && publicConfig.value && Date.now() - loadedAt < 30_000) return publicConfig.value;
  if (pending) return pending;
  const base = import.meta.env.SSR ? (import.meta.env.VITE_SSG_API || 'http://127.0.0.1:8000') : '';
  pending = (async () => {
    const result = await readJSON<PublicConfig>(`${base}/api/v1/storefront/config`);
    if (!validConfig(result)) throw new Error('店铺配置格式异常，请稍后重试');
    const next = { entries: mergeThemeConfig(result.entries) };
    if (JSON.stringify(publicConfig.value?.entries) !== JSON.stringify(next.entries)) publicConfig.value = next;
    loadedAt = Date.now();
    return next;
  })();
  try { return await pending; } finally { pending = undefined; }
}
