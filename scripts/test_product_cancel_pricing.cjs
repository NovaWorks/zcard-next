// Local browser regression fixtures. Build admin first; no real users, products, or upstream calls.
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
      const page = await browser.newPage({ viewport: { width, height: 1050 } }), errors = [], writes = [];
      let failSave = false, holdSave, releaseSave; const controls = [];
      const product = { id: 1, name: '关闭诊断商品', description: '<p>原始商品详情</p>', price_cents: 1000, status: 1, stock_type: 'card', fulfillment_mode: 'auto', manual_stock: -1, stock_visible: true, dedup: true };
      const connection = { id: 1, name: '定价测试渠道', driver: 'dujiao_next', base_url: 'https://fixture.invalid', exchange_rate: 1, price_markup_percent: 30, price_markup_amount: 500, price_rounding_mode: 'none', auto_sync_price: true, status: 'active', settings: '{}' };
      page.on('pageerror', e => errors.push(e.message));
      await page.addInitScript(() => { localStorage.setItem('token', JSON.stringify('fixture')); localStorage.setItem('refreshToken', JSON.stringify('fixture')); });
      await page.route('**/api/v1/**', async route => {
        const req = route.request(), p = new URL(req.url()).pathname, write = !['GET', 'HEAD'].includes(req.method());
        if (write) writes.push({ p, body: req.postDataJSON() });
        if (write && holdSave) await holdSave;
        if (write && failSave) return route.fulfill({ status: 500, json: { code: 500, reason: 'FIXTURE_FAILURE', message: '模拟保存失败' } });
        let data = { items: [], total: 0, categories: [], skus: [], controls: [], connections: [] };
        if (p.endsWith('/auth/profile')) data = { admin: { id: 1, username: 'fixture' }, permissions: ['*'] };
        if (p.endsWith('/captcha-config')) data = { enabled: false };
        if (p.endsWith('/products')) data = { products: [product], total: 1 };
        if (p.endsWith('/products/1')) { if (write) Object.assign(product, req.postDataJSON()); data = product; }
        if (p.endsWith('/products/1/controls')) { if (write) controls.push({ id: 1, ...req.postDataJSON() }); data = write ? controls.at(-1) : { controls }; }
        if (p.endsWith('/supply/connections')) data = { connections: [connection], total: 1 };
        if (p.endsWith('/supply/connections/1')) { if (write) Object.assign(connection, req.postDataJSON()); data = connection; }
        if (p.endsWith('/preview')) data = { categories: [{ code: 'cat1', name: '上游分类', products: [{ code: 'p1', name: '上游商品', price_cents: 1000 }] }], total: 1 };
        await route.fulfill({ json: data });
      });
      const modal = page.locator('.n-modal').filter({ hasText: '编辑商品' });
      const editor = page.locator('[contenteditable="true"]');
      const open = async () => { await page.getByRole('button', { name: '编辑', exact: true }).first().click(); await expect(modal).toBeVisible(); };
      const desc = async () => { await page.getByText('商品描述', { exact: true }).first().click(); await expect(editor).toBeVisible(); };
      const discard = async () => { await page.getByRole('button', { name: '放弃修改', exact: true }).click(); await expect(modal).not.toBeVisible(); };
      await page.goto(`http://127.0.0.1:${server.address().port}/admin/product`);
      await open(); await modal.locator('.n-base-close').first().click(); await expect(modal).not.toBeVisible();
      await open(); await desc(); await editor.fill('错误修改'); await modal.locator('.n-base-close').first().click();
      await page.getByRole('button', { name: '继续编辑', exact: true }).click(); await expect(page.getByRole('button', { name: '放弃修改', exact: true })).toHaveCount(0); await expect(editor).toContainText('错误修改');
      await page.keyboard.press('Escape'); await discard(); assert.equal(writes.length, 0);
      await open(); await desc(); await expect(editor).toContainText('原始商品详情'); await editor.fill('取消测试');
      await modal.getByRole('button', { name: '取消', exact: true }).click(); await discard(); assert.equal(writes.length, 0);
      await open(); await page.getByText('规格与控件', { exact: true }).click();
      await page.getByRole('button', { name: '+ 添加规格', exact: true }).click();
      await page.getByPlaceholder('规格名（如 时长）').fill('时长'); await modal.locator('.n-base-close').first().click(); await discard(); assert.equal(writes.length, 0);
      await open(); await desc(); await editor.fill('需要明确保存的内容'); await page.getByText('高级设置', { exact: true }).click();
      failSave = true; await modal.getByRole('button', { name: '保存', exact: true }).click();
      await expect.poll(() => writes.length).toBe(1); await expect(modal.getByRole('button', { name: '保存', exact: true })).toBeEnabled();
      await expect(modal).toBeVisible(); await desc(); await expect(editor).toContainText('需要明确保存的内容');
      failSave = false; holdSave = new Promise(r => { releaseSave = r; }); await page.getByText('高级设置', { exact: true }).click();
      await modal.getByRole('button', { name: '保存', exact: true }).click(); await expect.poll(() => writes.length).toBe(2);
      await expect(modal.getByRole('button', { name: '取消', exact: true })).toBeDisabled(); await expect(modal.locator('.n-base-close')).toHaveCount(0);
      await page.keyboard.press('Escape'); await expect(modal).toBeVisible(); releaseSave(); holdSave = null; await expect(modal).not.toBeVisible();
      await open(); await desc(); await expect(editor).toContainText('需要明确保存的内容'); await modal.getByRole('button', { name: '取消', exact: true }).click(); await expect(modal).not.toBeVisible();
      // Channel settings: errors retain the form; zero/false reach the API, and default import follows the channel.
      await page.goto(`http://127.0.0.1:${server.address().port}/admin/channel`);
      const more = () => page.getByRole('button', { name: /^(更多|操作)/ }).first();
      await more().click(); await page.getByText('编辑', { exact: true }).last().click();
      const channel = page.locator('.n-modal').filter({ hasText: '编辑渠道' });
      const markup = channel.locator('.n-form-item').filter({ hasText: '加价规则' }).locator('input');
      await expect(markup.nth(1)).toHaveValue('5.00');
      await markup.nth(0).fill('0'); await markup.nth(1).fill('0'); await channel.getByRole('switch').click();
      failSave = true; await channel.getByRole('button', { name: '保存', exact: true }).click();
      await expect.poll(() => writes.length).toBe(3); await expect(channel.getByRole('button', { name: '保存', exact: true })).toBeEnabled(); await expect(channel).toBeVisible();
      assert.equal(writes.at(-1).body.price_markup_percent, 0); assert.equal(writes.at(-1).body.price_markup_amount, 0); assert.equal(writes.at(-1).body.auto_sync_price, false);
      failSave = false; await channel.getByRole('button', { name: '保存', exact: true }).click(); await expect(channel).not.toBeVisible();
      await more().click(); await page.getByText('导入商品', { exact: true }).last().click();
      const importing = page.locator('.n-modal').filter({ hasText: '导入上游商品' });
      await expect(importing.getByText('跟随渠道定价', { exact: true })).toBeVisible();
      await expect(importing.getByText(/商品和规格使用同一规则/)).toBeVisible();
      await importing.getByRole('checkbox', { name: '选择上游分类全部商品' }).check();
      await importing.getByRole('button', { name: '保存并导入', exact: true }).click(); await expect(importing).not.toBeVisible();
      assert.equal(writes.at(-1).body.pricing_mode, 'channel');
      connection.settings = JSON.stringify({ import_pricing: { mode: 'percent', markup_percent: 0 } });
      await page.reload(); await more().click(); await page.getByText('导入商品', { exact: true }).last().click();
      await expect(importing.getByText('独立加价比例（%）', { exact: true })).toBeVisible();
      await expect(importing.getByText(/当前为独立导入策略/)).toBeVisible();
      await importing.getByRole('checkbox', { name: '选择上游分类全部商品' }).check();
      await importing.getByRole('button', { name: '保存并导入', exact: true }).click(); await expect(importing).not.toBeVisible();
      assert.equal(writes.at(-1).body.pricing_mode, 'percent'); assert.equal(writes.at(-1).body.markup_percent, 0);
      await page.goto(`http://127.0.0.1:${server.address().port}/admin/product`);
      await open(); await page.getByText('规格与控件', { exact: true }).click();
      await page.getByRole('button', { name: '新增控件', exact: true }).click();
      const controlName = page.getByPlaceholder('如：充值账号');
      await controlName.fill('频道链接'); const beforeControl = writes.length;
      await page.getByRole('button', { name: '收起', exact: true }).click();
      await page.getByText('高级设置', { exact: true }).click(); await modal.getByRole('button', { name: '保存', exact: true }).click();
      await expect(page.getByText(/下单控件还有未保存输入/)).toBeVisible(); assert.equal(writes.length, beforeControl);
      await page.getByText('规格与控件', { exact: true }).click(); await page.getByRole('button', { name: '新增控件', exact: true }).click();
      await page.getByRole('button', { name: '取消编辑', exact: true }).click();
      await page.getByRole('button', { name: '新增控件', exact: true }).click(); await expect(controlName).toHaveValue(''); await controlName.fill('频道链接');
      await modal.locator('.n-base-close').first().click(); await discard(); assert.equal(writes.length, beforeControl);
      await open(); await page.getByText('规格与控件', { exact: true }).click();
      await page.getByRole('button', { name: '新增控件', exact: true }).click(); await expect(controlName).toHaveValue(''); await controlName.fill('频道链接');
      await page.getByRole('button', { name: '创建控件', exact: true }).click(); await expect(modal.getByText(/已有内容单独保存并生效/)).toBeVisible();
      assert.equal(writes.length, beforeControl + 1);
      await desc(); await editor.fill('独立保存后未保存的修改'); await modal.locator('.n-base-close').first().click();
      await expect(page.getByText(/已单独保存或删除的规格、控件仍然生效/)).toBeVisible(); await discard();
      assert.equal(writes.length, beforeControl + 1); assert.equal(controls[0].name, '频道链接');
      if (width === 1440) {
        await page.goto(`http://127.0.0.1:${server.address().port}/admin/channel`);
        await page.getByRole('menuitem', { name: '商品管理', exact: true }).click(); await open();
        await page.getByPlaceholder('请输入商品名称').fill('离开页面测试');
        await page.evaluate(() => window.history.back()); await page.getByRole('button', { name: '继续编辑', exact: true }).click();
        await expect(page).toHaveURL(/\/admin\/product$/); await expect(page.getByRole('button', { name: '放弃修改', exact: true })).toHaveCount(0);
        await page.evaluate(() => window.history.back()); await discard(); await expect(page).toHaveURL(/\/admin\/channel$/);
      }
      assert.deepEqual(errors, []); await page.close(); console.log(`product cancel and channel pricing ${width}px: passed`);
    }
  } finally { await browser.close(); server.close(); }
})().catch(e => { console.error(e); server.close(); process.exitCode = 1; });
