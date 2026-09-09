/** The fullstack server supplies the current private entry in the document. */
export function adminBasePath(): string {
  if (import.meta.env.DEV) return import.meta.env.VITE_BASE_URL || "/admin/";
  return document.querySelector<HTMLMetaElement>('meta[name="zcard-admin-base"]')?.content || import.meta.env.VITE_BASE_URL || "/admin/";
}
