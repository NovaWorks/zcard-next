import { Boot, type IDomEditor } from "@wangeditor/editor";
import { pickVideo } from "../video-picker";
import { pickMedia } from "@/components/common/media-picker";

class MediaLibraryMenu {
  title = "素材库图片";
  iconSvg =
    '<svg viewBox="0 0 1024 1024" width="16" height="16"><path d="M896 128H128c-35.3 0-64 28.7-64 64v640c0 35.3 28.7 64 64 64h768c35.3 0 64-28.7 64-64V192c0-35.3-28.7-64-64-64z m0 704H128V192h768v640z" fill="currentColor"/><path d="M320 480m-64 0a64 64 0 1 0 128 0 64 64 0 1 0-128 0zM208 768h608v-64l-160-192-128 160-96-96z" fill="currentColor"/></svg>';
  tag = "button";

  getValue() {
    return "";
  }
  isActive() {
    return false;
  }
  isDisabled() {
    return false;
  }
  async exec(editor: IDomEditor) {
    const urls = await pickMedia({ multiple: true });
    if (!urls?.length) return;
    // dangerouslyInsertHtml：绕开 Slate Node 类型约束（wangEditor 官方推荐的 HTML 注入路径）
    editor.dangerouslyInsertHtml(urls.map((url) => `<img src="${url}" alt="" />`).join(""));
    editor.focus();
  }
}

class VideoMenu extends MediaLibraryMenu {
  title = "插入视频";
  iconSvg =
    '<svg aria-label="插入视频" role="img" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="4" width="20" height="16" rx="2"/><path d="m10 8 6 4-6 4Z"/></svg>';
  async exec(editor: IDomEditor) {
    const selection = editor.selection;
    const html = await pickVideo();
    if (!html || editor.isDestroyed) return;
    if (selection) editor.select(selection);
    else editor.focus(true);
    editor.dangerouslyInsertHtml(`<div data-w-e-type="video" data-w-e-is-void>${html}</div>`);
    editor.focus();
  }
}
let moduleRegistered = false;
export function registerMediaMenu() {
  if (moduleRegistered) return;
  Boot.registerModule({
    menus: [
      { key: "zcMediaLibrary", factory: () => new MediaLibraryMenu() as any },
      { key: "zcVideo", factory: () => new VideoMenu() as any },
    ],
  });
  moduleRegistered = true;
}
