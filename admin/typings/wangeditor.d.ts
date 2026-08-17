/**
 * @wangeditor/editor-for-vue 5.1.12 的 package.json exports 未正确指向类型文件
 * （vue-tsc 报 TS7016）——本地声明兜底；组件内用法仅 Editor/Toolbar 两个导出。
 */
declare module "@wangeditor/editor-for-vue" {
  import type { DefineComponent } from "vue";
  export const Editor: DefineComponent<Record<string, any>, Record<string, any>, any>;
  export const Toolbar: DefineComponent<Record<string, any>, Record<string, any>, any>;
}
