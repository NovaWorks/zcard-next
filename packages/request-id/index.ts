/** Random request identifiers also work on HTTP installations without randomUUID. */
export function newRequestId(): string {
  const provider = globalThis.crypto;
  if (typeof provider?.randomUUID === "function") return provider.randomUUID();
  if (typeof provider?.getRandomValues !== "function") {
    throw new Error("浏览器不支持安全生成请求标识，请更换浏览器后重试");
  }
  const bytes = provider.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}
