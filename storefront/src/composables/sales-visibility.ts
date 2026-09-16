import { computed, onMounted, ref } from 'vue';

// Public config includes the published theme overrides. Never render sales into
// shared SSG HTML or reveal them while the current shop's setting is unknown.
export function useSalesVisibility() {
  const enabled = ref(false);
  const mounted = ref(false);
  onMounted(() => { mounted.value = true; });

  function applySalesConfig(config: { entries?: { key: string; value_json: string }[] } | null) {
    enabled.value = false;
    if (!Array.isArray(config?.entries)) return;
    const entry = config.entries.find(e => e.key === 'template.show_sales');
    if (!entry) { enabled.value = true; return; } // Legacy config defaults to enabled.
    try { enabled.value = JSON.parse(entry.value_json) === true; } catch { /* Keep hidden. */ }
  }

  function normalizeSalesSort(value: string) {
    return value === 'sales' && !enabled.value ? 'default' : value;
  }

  return { showSales: computed(() => mounted.value && enabled.value), applySalesConfig, normalizeSalesSort };
}
