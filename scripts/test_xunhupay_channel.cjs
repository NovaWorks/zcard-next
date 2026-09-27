/** Built admin smoke test; disposable API fixtures, no merchant credentials. */
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
      const page = await browser.newPage({ viewport: { width, height: 1000 } });
      const errors = [], channels = [], updates = [];
      page.on('pageerror', e => errors.push(e.message));
      await page.addInitScript(() => { localStorage.setItem('token', JSON.stringify('local-test')); localStorage.setItem('refreshToken', JSON.stringify('local-refresh')); });
      await page.route('**/api/v1/**', async route => {
        const p = new URL(route.request().url()).pathname, method = route.request().method();
        let data = { items: [], total: 0 };
        if (p.endsWith('/auth/profile')) data = { admin: { id: 1, username: 'local-test' }, permissions: ['*'] };
        if (p.endsWith('/payment/drivers')) data = { drivers: [{ code: 'xunhupay', name: '虎皮椒支付', description: '虎皮椒微信 / 支付宝收款；不同 APPID 分别添加渠道', fields: [
          { key: 'appid', label: 'APPID', type: 'text', required: true },
          { key: 'appsecret', label: 'APPSECRET', type: 'password', required: true, sensitive: true },
          { key: 'api_url', label: '支付网关', type: 'text', default: 'https://api.xunhupay.com/payment/do.html', placeholder: '留空使用官方网关' },
        ] }] };
        if (p.endsWith('/payment/channels')) {
          if (method === 'POST') {
            data = { ...route.request().postDataJSON(), id: channels.length + 1, configured_fields: [], callback_url: 'https://shop.example/payments/callback/xunhupay' };
            channels.push(data);
          } else data = { channels };
        }
        if (/\/payment\/channels\/\d+$/.test(p) && method === 'PUT') {
          const body = route.request().postDataJSON(); updates.push(body);
          data = Object.assign(channels.find(c => c.id === Number(p.split('/').pop())), body);
          data.configured_fields = ['appid', 'appsecret'];
          data.config_json = JSON.stringify({ appid: JSON.parse(body.config_json).appid });
        }
        await route.fulfill({ json: data });
      });
      await page.goto(`http://127.0.0.1:${server.address().port}/admin/payment-channel`);
      for (const [index, name] of ['微信支付', '支付宝'].entries()) {
        await page.getByRole('button', { name: '添加渠道', exact: true }).click();
        const add = page.locator('.n-modal').filter({ hasText: '添加支付渠道' });
        await add.getByRole('checkbox').filter({ hasText: '虎皮椒支付' }).click();
        await add.getByRole('button', { name: /添加所选/ }).click();
        const config = page.locator('.n-modal').filter({ hasText: /配置「虎皮椒支付/ });
        await config.waitFor();
        const field = label => config.locator('.n-form-item').filter({ has: page.locator('.n-form-item-label', { hasText: label }) }).locator('input');
        await field('渠道名称').fill(name);
        await field('APPID').fill(`fixture-app-${index}`);
        await field('APPSECRET').fill(`fixture-secret-${index}`);
        assert.equal(await field('APPSECRET').getAttribute('type'), 'password');
        assert(!await config.getByText('按模板生成', { exact: true }).count());
        const box = await config.boundingBox();
        assert(box.x >= 0 && box.x + box.width <= width + 1, 'modal overflows');
        await config.getByRole('button', { name: '保存', exact: true }).click();
        await config.waitFor({ state: 'hidden' });
        assert.equal(JSON.parse(updates.at(-1).config_json).appid, `fixture-app-${index}`);
        assert.equal(channels[index].driver, 'xunhupay');
        assert(!channels[index].methods_json);
      }
      assert.notEqual(channels[0].code, channels[1].code);
      await page.reload();
      await page.getByText('微信支付', { exact: true }).waitFor();
      await page.getByText('支付宝', { exact: true }).waitFor();
      assert.deepEqual(errors, []);
      await page.close();
      console.log(`PASS xunhupay independent WeChat/Alipay configuration width=${width}`);
    }
  } finally { await browser.close(); server.close(); }
})().catch(e => { console.error(e); server.close(); process.exitCode = 1; });
