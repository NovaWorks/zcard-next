<template>
  <!-- 客服代码（service.widget_script）：第三方客服脚本自带悬浮球，本站不渲染 UI -->
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

/** 提取嵌入代码中的 <script> 内容并执行（DOMParser 提取，防 XSS 依赖后端 sanitize/管理员自配） */
let injected = false;
function injectScript(html: string) {
  if (injected) return;
  injected = true;
  try {
    const doc = new DOMParser().parseFromString(html, 'text/html');
    const scripts = Array.from(doc.querySelectorAll('script'));
    if (!scripts.length) return;
    for (const s of scripts) {
      const code = s.textContent || '';
      if (!code.trim()) continue;
      const el = document.createElement('script');
      el.textContent = code;
      el.dataset.zcardServiceWidget = 'true';
      document.head.appendChild(el);
    }
  } catch { /* 脚本注入失败忽略（客服功能降级为不可用） */ }
}

onMounted(async () => {
  try {
    const resp = await fetch('/api/v1/storefront/config');
    const json = await resp.json();
    const entry = (json?.entries || []).find((e: any) => e.key === 'service.widget_script');
    let script = '';
    if (entry) {
      try { script = JSON.parse(entry.value_json); } catch { script = entry.value_json; }
    }
    if (typeof script === 'string' && script.trim()) injectScript(script);
  } catch { /* 配置接口失败：客服不显示 */ }
});
</script>
