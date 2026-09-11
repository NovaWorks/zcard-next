import { request } from "../request";
export function fetchThemeSettings(key: string, theme_revision = "") {
  return request<{ state_json: string }>({
    url: `/api/v1/admin/settings/themes/${key}`,
    params: { theme_revision },
  });
}
export function saveThemeSettings(key: string, data: Record<string, unknown>) {
  return request<{ state_json: string }>({
    url: `/api/v1/admin/settings/themes/${key}`,
    method: "put",
    data,
  });
}
export function previewThemeSettings(
  key: string,
  data: Record<string, unknown>,
) {
  return request<{ url: string; expires_at: number }>({
    url: `/api/v1/admin/settings/themes/${key}/preview`,
    method: "post",
    data,
  });
}
