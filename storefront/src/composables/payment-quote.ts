import { ref, watch, onScopeDispose } from 'vue';
import { quotePayment, type PaymentQuote, type PaymentQuoteRequest } from '@/api';

export function usePaymentQuote(request: () => PaymentQuoteRequest | null) {
  const quote = ref<PaymentQuote | null>(null);
  const loading = ref(false);
  const error = ref('');
  let revision = 0;
  async function refresh() {
    const version = ++revision;
    const body = request();
    quote.value = null; error.value = ''; loading.value = !!body;
    if (!body) return;
    try {
      const result = await quotePayment(body);
      if (version !== revision) return;
      if (result.error || !result.data) { error.value = result.error || '暂时无法计算支付金额，请重试'; return; }
      const data = result.data;
      quote.value = { ...data, base_cents: Number(data.base_cents || 0), fee_cents: Number(data.fee_cents || 0), total_cents: Number(data.total_cents || 0), fee_rate: Number(data.fee_rate || 0) };
    } catch { if (version === revision) error.value = '暂时无法计算支付金额，请重试'; }
    finally { if (version === revision) loading.value = false; }
  }
  watch(() => JSON.stringify(request()), refresh, { immediate: true });
  onScopeDispose(() => { revision++; });
  return { quote, quoteLoading: loading, quoteError: error, refreshQuote: refresh };
}
