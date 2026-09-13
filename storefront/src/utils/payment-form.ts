// Reopening a POST checkout must retain its signed parameters, not navigate to the bare gateway.
export function submitPaymentForm(url: string, params: Record<string, string>) {
  const target = new URL(url);
  if (!['https:', 'http:'].includes(target.protocol) || !params || !Object.keys(params).length) {
    throw new Error('支付参数异常，请重新选择支付方式');
  }
  const form = document.createElement('form');
  form.action = target.href;
  form.method = 'POST';
  form.target = '_blank';
  form.rel = 'noopener';
  for (const [name, value] of Object.entries(params)) {
    const input = document.createElement('input');
    input.type = 'hidden'; input.name = name; input.value = String(value);
    form.appendChild(input);
  }
  document.body.appendChild(form);
  try { form.submit(); } finally { form.remove(); }
}
