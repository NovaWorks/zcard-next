/** Local mock APIs only. Build storefront before running. */
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http');
const root = path.resolve(__dirname, '../storefront/dist');
let balance = 1000, status = 'pending', polls = 0, accountReads = 0, hold = false, release;
const account = () => ({ id: 1, protocol: 'zcard', status: 'approved', display_name: '共用钱包测试', balance_cache: balance, shared_wallet: true });
const server = http.createServer(async (req, res) => {
 const url = new URL(req.url, 'http://localhost');
 if (url.pathname.startsWith('/api/')) {
  res.setHeader('Content-Type', 'application/json');
  let data = { items: [], entries: [], total: 0 };
  if (url.pathname.endsWith('/config')) data = { entries: [{ key: 'site.name', value_json: '"本地验收"' }] };
  if (url.pathname.endsWith('/supplier/accounts')) { accountReads++; data = { accounts: [account()] }; }
  if (url.pathname.endsWith('/payment/channels')) data = { channels: [{ code: 'test', name: '测试支付', driver: 'epay' }] };
  if (url.pathname.endsWith('/payment/quote')) data = { base_cents: 20000, fee_cents: 0, total_cents: 20000, quote_key: 'local', channel: 'test', method: '' };
  if (url.pathname.endsWith('/supplier/accounts/1/recharge')) data = { recharge_id: 42, payment_id: 9, type: 'redirect', payload: 'https://payment.example.test/' };
  if (url.pathname.endsWith('/supplier/accounts/1/recharges/42')) {
   polls++;
   if (hold) await new Promise(resolve => { release = resolve; });
   data = { recharge_id: 42, payment_id: 9, status, credited_cents: status === 'success' ? 22000 : 0 };
  }
  return res.end(JSON.stringify(data));
 }
 let file = path.join(root, url.pathname);
 if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
 res.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css' })[path.extname(file)] || 'application/octet-stream');
 res.end(fs.readFileSync(file));
});
(async () => {
 await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
 const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) });
 try {
  const page = await browser.newPage(); const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(() => localStorage.setItem('zcard_token', 'local-fixture'));
  await page.goto(`http://127.0.0.1:${server.address().port}/member?tab=supplier`);
  async function open() { await page.locator('.account-actions').getByRole('button', { name: '充值', exact: true }).click(); await page.waitForFunction(() => document.querySelector('.recharge-submit') && !document.querySelector('.recharge-submit').disabled); }
  await open();
  assert.match(await page.locator('.recharge-head').textContent(), /账户余额充值/);
  await page.locator('.recharge-submit').click();
  balance = 9000; const initialReads = accountReads;
  await page.waitForTimeout(3800);
  assert(polls > 0, 'must query recharge order');
  assert.equal(accountReads, initialReads, 'must not poll wallet balance');
  assert.equal(await page.locator('.recharge-done').count(), 0, 'unrelated credit is not recharge success');
  status = 'success'; balance += 22000;
  await page.locator('.recharge-done').waitFor();
  assert.match(await page.locator('.recharge-done').textContent(), /220\.00/, 'display order credit, not wallet delta');
  await page.locator('.recharge-close').click();
  status = 'pending'; hold = true; release = null;
  await open(); await page.locator('.recharge-submit').click();
  for (let i = 0; !release && i < 50; i++) await page.waitForTimeout(100);
  assert(release, 'delayed status request must start');
  await page.locator('.recharge-close').click(); await open();
  status = 'success'; hold = false; release();
  await page.waitForTimeout(350);
  assert.equal(await page.locator('.recharge-done').count(), 0, 'late reply must not mark reopened dialog paid');
  await page.locator('.recharge-close').click();
  const stopped = polls; await page.waitForTimeout(3300); assert.equal(polls, stopped, 'close must stop polling');
  assert.deepEqual(errors, []);
  console.log('PASS shared-wallet recharge: order status, credited amount, late response, timer cleanup');
 } finally { if (release) release(); await browser.close(); server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }
})().catch(error => { console.error(error); process.exitCode = 1; });
