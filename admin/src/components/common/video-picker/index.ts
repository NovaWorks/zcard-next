import { reactive } from "vue";
export const videoPicker = reactive({
  show: false,
  resolve: null as ((html: string | null) => void) | null,
});
export function pickVideo(): Promise<string | null> {
  videoPicker.resolve?.(null);
  videoPicker.show = true;
  return new Promise((resolve) => {
    videoPicker.resolve = resolve;
  });
}
export function closeVideo(html: string | null = null) {
  videoPicker.show = false;
  videoPicker.resolve?.(html);
  videoPicker.resolve = null;
}
export function videoHTML(src: string, poster: string): string {
  const escape = (s: string) =>
    s.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return `<video controls playsinline preload="none" src="${escape(src)}"${poster ? ` poster="${escape(poster)}"` : ""}></video>`;
}
export function validVideoURL(value: string): boolean {
  if (/^\/uploads\/[^\s?#]+$/.test(value)) return true;
  try {
    const u = new URL(value);
    return u.protocol === "https:" && !u.username && !u.password;
  } catch {
    return false;
  }
}
