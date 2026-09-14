/** 图片路径与 emoji 共用分类 icon 字段；相对图片路径按站点根目录解析。 */
export function categoryIconImagePath(icon?: string): string {
  const value = icon?.trim() || "";
  if (!value || (!value.includes("/") && !/\.(png|jpe?g|gif|webp|svg|ico|bmp|avif)(?:[?#].*)?$/i.test(value))) return "";
  if (value.startsWith("/") || /^(https?:|data:image\/)/i.test(value)) return value;
  return `/${value.replace(/^\.\//, "")}`;
}
