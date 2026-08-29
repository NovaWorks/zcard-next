// 方式级收银台共享逻辑：渠道 → 顾客可见的支付方式选项（Payment.vue / Member.vue 充值同源）。
import type { ChannelItem } from '@/api';

export interface PayOption {
  channel: string;
  method: string; // 多方式渠道的方式 code；单方式渠道为空串
  name: string;
  icon?: string;
  emoji: string;
  sub: string;
}

// 方式/渠道内置 emoji 回落（未配置自定义图标时）
const EMOJI: Record<string, string> = {
  wallet: '💰', alipay: '🅰️', wxpay: '💬', wechat: '💬', qqpay: '🐧',
  epay: '⚡', epusdt: '₮', stripe: '🟦', paypal: '🅿️',
};

export function emojiOf(code: string, driver: string) {
  return EMOJI[code] || EMOJI[driver] || '💳';
}

// 渠道 → 收银台方式级选项展平：多方式渠道（易支付/USDT 网关）每个方式一个选项，
// 顾客看到的是「支付宝 / 微信 / USDT·TRC20」而不是网关本身；单方式渠道保持原样。
export function flattenPayOptions(channels: ChannelItem[]): PayOption[] {
  const out: PayOption[] = [];
  for (const c of channels) {
    const methods = (c.methods || []).filter((m) => m.name);
    if (methods.length > 0) {
      for (const m of methods) {
        out.push({
          channel: c.code, method: m.code, name: m.name,
          icon: m.icon || c.icon || undefined, emoji: emojiOf(m.code, c.driver),
          sub: c.driver === 'epusdt' ? 'USDT 链上收款' : '在线支付',
        });
      }
    } else {
      out.push({
        channel: c.code, method: '', name: c.name,
        icon: c.icon || undefined, emoji: emojiOf(c.code, c.driver),
        sub: c.driver === 'wallet' ? '使用账户余额' : '在线支付',
      });
    }
  }
  return out;
}
