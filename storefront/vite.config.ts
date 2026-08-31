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
        target: 'http://127.0.0.1:8000',
        changeOrigin: true
      },
      // 素材/上传文件（商品封面、背景图等）——产物环境由 Go 直接服务
      '/uploads': {
        target: 'http://127.0.0.1:8000',
        changeOrigin: true
      }
    }
  },
  ssgOptions: {
    // 串行渲染：并发下 unhead 2 的渲染队列与 seo.ts 模块级 head 引用会跨路由串扰
    // （详见 src/seo.ts 注释）。串行后 unhead renderDOMHead 输出完整 head。
    concurrency: 1,
  }
});
