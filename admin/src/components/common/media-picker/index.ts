import { reactive } from "vue";

/**
 * 素材选择器全局单例（模块级状态，App.vue 挂载 <MediaPickerHost /> 一次）。
 *
 * 用法：const urls = await pickMedia({ multiple: true }); // 取消返回 null
 * 所有「上传/选择图片」入口统一呼出素材管理弹窗——复用已有素材 + 就地上传。
 */

interface MediaPickerState {
  show: boolean;
  multiple: boolean;
  resolve: ((urls: string[] | null) => void) | null;
}

export const mediaPickerState = reactive<MediaPickerState>({
  show: false,
  multiple: false,
  resolve: null,
});

export interface PickMediaOptions {
  /** 多选（图集）；默认单选 */
  multiple?: boolean;
}

export function pickMedia(options: PickMediaOptions = {}): Promise<string[] | null> {
  // 二次打开时废弃上一个等待（防悬挂 Promise）
  mediaPickerState.resolve?.(null);
  mediaPickerState.multiple = options.multiple ?? false;
  return new Promise((resolve) => {
    mediaPickerState.resolve = resolve;
    mediaPickerState.show = true;
  });
}

/** 弹窗内部专用：确认/取消回填 */
export function settleMediaPicker(urls: string[] | null) {
  mediaPickerState.show = false;
  mediaPickerState.resolve?.(urls);
  mediaPickerState.resolve = null;
}
