import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import { fileURLToPath, URL } from 'node:url';

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url))
    }
  },
  server: {
    port: 9528,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:18000',
        changeOrigin: true
      }
    }
  }
});
