// 商品无图默认占位（SVG data URI：灰底 + 🖼 + 「暂无主图」）。
// 用于无 cover 与 cover 加载失败（@error）两种场景，避免裂图/空白。

export const NO_IMAGE =
  'data:image/svg+xml;charset=utf-8,' +
  encodeURIComponent(
    "<svg xmlns='http://www.w3.org/2000/svg' width='400' height='400'>" +
      "<rect width='100%' height='100%' fill='#f1f5f9'/>" +
      "<rect x='1' y='1' width='398' height='398' fill='none' stroke='#e2e8f0' stroke-width='2'/>" +
      "<text x='50%' y='44%' font-size='40' fill='#cbd5e1' text-anchor='middle' dominant-baseline='middle'>🖼️</text>" +
      "<text x='50%' y='60%' font-family='-apple-system,PingFang SC,sans-serif' font-size='24' fill='#94a3b8' text-anchor='middle' dominant-baseline='middle'>暂无主图</text>" +
      '</svg>',
  );

// onImgError 图片加载失败 → 换占位图（data URI 不会再失败，无重入风险）。
export function onImgError(e: Event) {
  const img = e.target as HTMLImageElement;
  if (img.src !== NO_IMAGE) img.src = NO_IMAGE;
}
