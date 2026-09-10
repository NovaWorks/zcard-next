import { Boot, type IDomEditor } from "@wangeditor/editor";
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

let moduleRegistered = false;
export function registerMediaMenu() {
  if (moduleRegistered) return;
  Boot.registerModule({
    menus: [{ key: "zcMediaLibrary", factory: () => new MediaLibraryMenu() as any }],
  });
  moduleRegistered = true;
}
