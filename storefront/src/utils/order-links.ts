// Historical purchase values are shown verbatim; only HTTP(S) values become links.
export function orderTargetLink(value?: string): string | undefined {
  if (!value || /\s/.test(value)) return undefined;
  try {
    const url = new URL(value);
    if (!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password) return undefined;
    return value;
  } catch { return undefined; }
}
