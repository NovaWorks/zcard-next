// 推广归因捕获：URL ?ref= → localStorage（30 天 TTL）。
// 任何页面进站即捕获（不依赖注册页）；注册预填/下单带码统一读取。

const REF_KEY = 'zc_ref';
const REF_TTL_MS = 30 * 24 * 60 * 60 * 1000; // 30 天

interface RefRecord {
  code: string;
  expires: number;
}

/** 捕获 URL 中的 ?ref=（App 挂载时调用一次） */
export function captureRefCode() {
  try {
    const params = new URLSearchParams(location.search);
    const ref = params.get('ref');
    if (ref && ref.trim()) {
      const rec: RefRecord = { code: ref.trim(), expires: Date.now() + REF_TTL_MS };
      localStorage.setItem(REF_KEY, JSON.stringify(rec));
    }
  } catch { /* 存储不可用忽略 */ }
}

/** 读取有效归因码（过期清除；空返回 ''） */
export function getRefCode(): string {
  try {
    const raw = localStorage.getItem(REF_KEY);
    if (!raw) return '';
    const rec = JSON.parse(raw) as RefRecord;
    if (!rec?.code || !rec?.expires || rec.expires < Date.now()) {
      localStorage.removeItem(REF_KEY);
      return '';
    }
    return rec.code;
  } catch {
    return '';
  }
}

/** 清除归因（注册成功消费后可清；保留亦可——下单仍可归因） */
export function clearRefCode() {
  try {
    localStorage.removeItem(REF_KEY);
  } catch { /* 忽略 */ }
}
