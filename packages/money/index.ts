/** DB/API amounts are always hundredths of the base currency. Display precision
 * and exchange rates must never change form input/output units. */
export function displayPrecision(value: unknown): number {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0 && value <= 8 ? value : 2;
}

function decimalParts(value: string): { numerator: bigint; denominator: bigint } {
  const match = /^([+-]?)(\d+)(?:\.(\d+))?(?:e([+-]?\d+))?$/i.exec(value.trim());
  if (!match || value.length > 100) throw new RangeError('金额格式不正确');
  const exponent = Number(match[4] || 0) - (match[3]?.length || 0);
  if (!Number.isInteger(exponent) || Math.abs(exponent) > 30) throw new RangeError('金额超出范围');
  let numerator = BigInt(match[2] + (match[3] || '')) * (match[1] === '-' ? -1n : 1n);
  let denominator = 1n;
  if (exponent >= 0) numerator *= 10n ** BigInt(exponent);
  else denominator = 10n ** BigInt(-exponent);
  return { numerator, denominator };
}

export function toCents(value: number | string): number {
  const { numerator, denominator } = decimalParts(String(value));
  const scaled = numerator * 100n;
  if (scaled % denominator !== 0n) throw new RangeError('金额最多精确到分（小数点后两位）');
  const cents = Number(scaled / denominator);
  if (!Number.isSafeInteger(cents)) throw new RangeError('金额超出范围');
  return cents;
}

export function fromCents(value: number): number {
  return Number.isSafeInteger(value) ? value / 100 : 0;
}

export function formatCents(value: number, precision: number, rate = '1'): string {
  const digits = displayPrecision(precision);
  const cents = BigInt(Number.isSafeInteger(value) ? value : 0);
  let exchange = { numerator: 1n, denominator: 1n };
  try { const parsed = decimalParts(rate); if (parsed.numerator > 0n) exchange = parsed; } catch { /* same-currency fallback */ }
  const numerator = cents * exchange.numerator * 10n ** BigInt(digits);
  const denominator = 100n * exchange.denominator;
  const abs = numerator < 0n ? -numerator : numerator;
  const rounded = (abs * 2n + denominator) / (denominator * 2n);
  const str = rounded.toString().padStart(digits + 1, '0');
  const body = digits ? `${str.slice(0, -digits)}.${str.slice(-digits)}` : str;
  return numerator < 0n && rounded !== 0n ? `-${body}` : body;
}
