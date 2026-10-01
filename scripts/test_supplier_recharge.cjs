// Built admin UI and disposable API fixtures; never touches a real balance.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http'), assert = require('node:assert/strict');
const root = path.resolve(__dirname, '../admin/dist');
const server = http.createServer((req, res) => {
  let file = path.join(root, new URL(req.url, 'http://localhost').pathname.replace(/^\/admin\/?/, ''));
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
  res.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'application/javascript', '.css': 'text/css', '.svg': 'image/svg+xml' })[path.extname(file)] || 'application/octet-stream');
  res.end(fs.readFileSync(file));
});
(async () => {
  await new Promise(r => server.listen(0, '127.0.0.1', r));
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) });
  try {
    for (const width of [1440, 390]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } }), errors = [], requests = [];
      page.setDefaultTimeout(10000);
      page.on('pageerror', e => errors.push(e.message));
      await page.addInitScript(() => localStorage.setItem('token', JSON.stringify('fixture')));
      const account = { id: 3, name: 'kmigo测试', protocol: 'zcard', status: 'approved', balance_cache: 0 };
      let fail = true;
      await page.route('**/api/v1/**', async route => {
        const req = route.request(), p = new URL(req.url()).pathname;
        let data = { items: [], total: 0 };
        if (p.endsWith('/auth/profile')) data = { admin: { id: 1, username: 'fixture' }, permissions: ['*'] };
        if (p.endsWith('/supplier/accounts')) data = { accounts: [account], total: 1 };
        if (p.endsWith('/supplier/accounts/3/recharge')) {
          requests.push(req.postDataJSON());
          if (fail) { fail = false; return route.fulfill({ status: 503, json: { code: 503, message: '测试重试' } }); }
          account.balance_cache += Number(requests.at(-1).amount_cents);
          data = {};
        }
        await route.fulfill({ json: data });
      });
      await page.goto(`http://127.0.0.1:${server.address().port}/admin/channel?tab=suppliers`);
      const open = async () => {
        const actions = page.getByRole('button', { name: '操作 ▾', exact: true });
        if (await actions.count()) {
          await actions.click();
          await page.locator('.n-dropdown-option-body').filter({ hasText: /^充值$/ }).click();
        } else await page.getByRole('button', { name: '充值', exact: true }).click();
      };
      await page.getByText('kmigo测试', { exact: true }).waitFor();
      await open();
      const modal = page.locator('.n-modal').filter({ hasText: '充值：kmigo测试' });
      await modal.waitFor();
      const amount = modal.locator('.n-form-item').filter({ hasText: '充值金额（元）' }).locator('input');
      const submit = modal.getByRole('button', { name: '充值', exact: true });
      await submit.click();
      assert.equal(requests.length, 0, 'empty amount sent');
      await amount.fill('30');
      await amount.blur();
      await submit.click();
      await page.getByText('测试重试', { exact: true }).waitFor();
      assert.equal(requests[0].amount_cents, 3000, 'yuan not converted to amount_cents');
      assert(!('amount' in requests[0]), 'obsolete amount field still sent');
      assert.match(requests[0].reference, /^[0-9a-f]{32}$/);
      await submit.click();
      await modal.waitFor({ state: 'hidden' });
      assert.equal(requests[1].reference, requests[0].reference, 'retry changed idempotency key');
      await open();
      await modal.waitFor();
      await amount.fill('30');
      await amount.blur();
      await submit.click();
      await modal.waitFor({ state: 'hidden' });
      assert.notEqual(requests[2].reference, requests[1].reference, 'new equal amount deposit reused key');
      assert.equal(account.balance_cache, 6000);
      assert.deepEqual(errors, []);
      console.log(`PASS width=${width}: 30 yuan => 3000 cents; retry keeps reference; next equal deposit gets new reference`);
      await page.close();
    }
  } finally { await browser.close(); await new Promise(r => server.close(r)); }
})().catch(e => { console.error(e); server.close(); process.exitCode = 1; });
