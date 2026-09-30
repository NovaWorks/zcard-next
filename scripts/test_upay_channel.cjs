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
        if (p.endsWith('/payment/drivers')) data = { drivers: [{ code: 'upay', name: 'UPAY PRO', description: 'UPAY PRO CNY 计价数字货币收款', fields: [
          { key: 'api_url', label: '网关地址', type: 'text', required: true },
          { key: 'secret_key', label: '通信密钥', type: 'password', required: true, sensitive: true },
          { key: 'trade_type', label: '收款币种与网络', type: 'select', default: 'USDT-TRC20', required: true, options: [{label:'USDT · TRC20',value:'USDT-TRC20'},{label:'USDC · BSC',value:'USDC-BSC'}], help: '网关需配置对应钱包和汇率。' },
          { key: 'timeout', label: '本地支付期限（秒）', type: 'number', default: '1200', help: '60–3600 秒；网关期限需在 UPAY PRO 后台另行设置。' },
        ] }] };
        if (p.endsWith('/payment/channels')) {
          if (method === 'POST') {
            data = { ...route.request().postDataJSON(), id: channels.length + 1, configured_fields: [], callback_url: 'https://shop.example/payments/callback/upay' };
            channels.push(data);
          } else data = { channels };
        }
        if (/\/payment\/channels\/\d+$/.test(p) && method === 'PUT') {
          const body = route.request().postDataJSON(); updates.push(body);
          data = Object.assign(channels.find(c => c.id === Number(p.split('/').pop())), body);
          data.configured_fields = ['api_url', 'secret_key'];
          data.config_json = JSON.stringify({ api_url: JSON.parse(body.config_json).api_url });
        }
        await route.fulfill({ json: data });
      });
      await page.goto(`http://127.0.0.1:${server.address().port}/admin/payment-channel`);
      for (const [index, name] of ['USDT TRC20', 'USDC BSC'].entries()) {
        await page.getByRole('button', { name: '添加渠道', exact: true }).click();
        const add = page.locator('.n-modal').filter({ hasText: '添加支付渠道' });
        await add.getByRole('checkbox').filter({ hasText: 'UPAY PRO' }).click();
        await add.getByRole('button', { name: /添加所选/ }).click();
        const config = page.locator('.n-modal').filter({ hasText: /配置「UPAY PRO/ });
        await config.waitFor();
        const field = label => config.locator('.n-form-item').filter({ has: page.locator('.n-form-item-label', { hasText: label }) }).locator('input');
        await field('渠道名称').fill(name);
        await field('网关地址').fill(`https://pay${index}.example`);
        await field('通信密钥').fill(`fixture-secret-${index}`);
        assert.equal(await field('通信密钥').getAttribute('type'), 'password');
        assert.equal(await field('本地支付期限').inputValue(), '1200');
        if (index === 1) {
          await config.locator('.n-select').click();
          await page.getByText('USDC · BSC', { exact: true }).click();
        }
        await config.getByText('网关需配置对应钱包和汇率。', { exact: true }).waitFor();
        assert(!await config.getByText('按模板生成', { exact: true }).count());
        const box = await config.boundingBox();
        assert(box.x >= 0 && box.x + box.width <= width + 1, 'modal overflows');
        await config.getByRole('button', { name: '保存', exact: true }).click();
        await config.waitFor({ state: 'hidden' });
        assert.equal(JSON.parse(updates.at(-1).config_json).api_url, `https://pay${index}.example`);
        assert.equal(channels[index].driver, 'upay');
        assert.equal(JSON.parse(updates.at(-1).config_json).trade_type, index === 0 ? 'USDT-TRC20' : 'USDC-BSC');
        assert.equal(JSON.parse(updates.at(-1).config_json).timeout, 1200);
        assert(!channels[index].methods_json);
      }
      assert.notEqual(channels[0].code, channels[1].code);
      await page.reload();
      await page.getByText('USDT TRC20', { exact: true }).waitFor();
      await page.getByText('USDC BSC', { exact: true }).waitFor();
      assert.deepEqual(errors, []);
      await page.close();
      console.log(`PASS upay independent network configuration width=${width}`);
    }
  } finally { await browser.close(); server.close(); }
})().catch(e => { console.error(e); server.close(); process.exitCode = 1; });
