// Local fixtures only: account costs, lazy quoting, retry and stale modal responses.
const fs = require('node:fs'), path = require('node:path'), http = require('node:http'), assert = require('node:assert/strict');
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const root = path.resolve(__dirname, '../admin/dist');
const server = http.createServer((req, res) => {
  let file = path.join(root, new URL(req.url, 'http://localhost').pathname.replace(/^\/admin\/?/, ''));
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
  res.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'application/javascript', '.css': 'text/css' })[path.extname(file)] || 'application/octet-stream');
  res.end(fs.readFileSync(file));
});
(async () => {
  await new Promise(r => server.listen(0, '127.0.0.1', r));
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) });
  try {
    for (const width of [1440, 390]) {
      const page = await browser.newPage({ viewport: { width, height: 1050 } });
      const errors = [], calls = [], writes = [];
      let active = 0, maxActive = 0, p2Attempts = 0, p1Cost = 1275, held, release;
      const hold = () => { held = new Promise(r => { release = r; }); };
      hold();
      const connection = { id: 1, name: '账号报价渠道', driver: 'acg_faka', base_url: 'https://up.example.com', status: 'active',
        exchange_rate: 1.5, price_markup_percent: 0, price_markup_amount: 0, price_rounding_mode: 'none', auto_sync_price: true, settings: '{}' };
      const product = code => ({ code, name: `账号商品${code}`, price_cents: 9900, factory_price_cents: 9900,
        cost_price_cents: -1, quote_status: 'pending', stock: 10, is_active: true, already_imported: code === 'p1' });
      const reply = products => ({ categories: [{ code: 'cat', name: '上游分类', products }], total: products.length });
      page.on('pageerror', e => errors.push(e.message));
      await page.addInitScript(() => { localStorage.setItem('token', JSON.stringify('fixture')); localStorage.setItem('refreshToken', JSON.stringify('fixture-refresh')); });
      await page.route('**/api/v1/**', async route => {
        const req = route.request(), url = new URL(req.url()), p = url.pathname;
        if (req.method() !== 'GET') writes.push(p);
        let data = { items: [], total: 0, categories: [], connections: [] };
        if (p.endsWith('/auth/profile')) data = { admin: { id: 1, username: 'fixture' }, permissions: ['*'] };
        if (p.endsWith('/captcha-config')) data = { enabled: false };
        if (p.endsWith('/supply/connections')) data = { connections: [connection], total: 1 };
        if (p.endsWith('/preview')) {
          const code = url.searchParams.get('quote_code');
          if (!code) {
            data = reply(['p1', 'p2', 'p3'].map(product));
            data.categories.push({ code: 'hidden', name: '未展开分类', products: [product('p4')] });
          } else {
            calls.push(code); active++; maxActive = Math.max(maxActive, active);
            const cost = p1Cost;
            if (code === 'p1' && held) await held;
            const failed = code === 'p2' && ++p2Attempts === 1;
            data = reply([{ ...product(code), quote_status: failed ? 'failed' : 'ready',
              cost_price_cents: failed ? -1 : code === 'p1' ? cost : 900, cost_is_minimum: code === 'p1' }]);
            active--;
          }
        }
        await route.fulfill({ json: data }).catch(() => {}); // An intentionally cancelled old modal request may close its route.
      });
      await page.goto(`http://127.0.0.1:${server.address().port}/admin/channel`);
      const modal = page.locator('.n-modal').filter({ hasText: '导入上游商品' });
      const open = async () => {
        await page.getByRole('button', { name: /^(更多|操作)/ }).first().click();
        await page.getByText('导入商品', { exact: true }).last().click();
        await expect(modal.getByRole('button', { name: '展开上游分类', exact: true })).toBeVisible();
      };
      await open(); assert.equal(calls.length, 0, 'collapsed catalog must not quote every product');
      await modal.getByRole('button', { name: '展开上游分类', exact: true }).click();
      await expect(modal.getByRole('checkbox', { name: /账号商品p1成本查询中/ })).toBeVisible();
      await expect(modal.getByRole('button', { name: '重试账号商品p2成本' })).toBeVisible();
      assert(!await modal.locator('.product-list').innerText().then(t => t.includes('99.00')), 'raw list price leaked');
      release(); held = null;
      await expect(modal.getByText(/成本.*12\.75.*起/)).toBeVisible();
      await modal.getByRole('button', { name: '重试账号商品p2成本' }).click();
      await expect(modal.getByRole('button', { name: '重试账号商品p2成本' })).toHaveCount(0);
      await expect(modal.getByText(/已选 0 件/)).toBeVisible();
      assert.equal(maxActive, 2); assert(!calls.includes('p4'));
      await modal.getByRole('checkbox', { name: /账号商品p1/ }).check();
      p1Cost = 1375;
      await modal.getByRole('button', { name: '刷新成本', exact: true }).click();
      await expect(modal.getByText(/成本.*13\.75.*起/)).toBeVisible();
      await expect(modal.getByText(/已选 1 件/)).toBeVisible();
      // Hold an obsolete request, close/reopen, and ensure it cannot overwrite the new result.
      p1Cost = 99900; hold();
      const before = calls.length;
      await modal.getByRole('button', { name: '刷新成本', exact: true }).click();
      await expect.poll(() => calls.length).toBeGreaterThan(before);
      await modal.getByRole('button', { name: '取消', exact: true }).click();
      await expect(modal).not.toBeVisible();
      const releaseOld = release; held = null; p1Cost = 1425;
      await open(); await modal.getByRole('button', { name: '展开上游分类', exact: true }).click();
      await expect(modal.getByText(/成本.*14\.25.*起/)).toBeVisible();
      releaseOld();
      await expect.poll(() => active).toBe(0);
      await expect(modal.getByText(/成本.*14\.25.*起/)).toBeVisible();
      assert(!await modal.innerText().then(t => t.includes('999.00')));
      await expect(page.getByText('canceled', { exact: true })).toHaveCount(0);
      await expect(modal.locator('.n-spin-body')).not.toBeVisible();
      await page.screenshot({ path: `/tmp/zcard-preview-costs-${width}.png`, fullPage: true });
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false);
      assert.deepEqual(writes, []); assert.deepEqual(errors, []);
      await page.close(); console.log(`PASS import account cost preview ${width}px`);
    }
  } finally { await browser.close(); server.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; server.close(); });
