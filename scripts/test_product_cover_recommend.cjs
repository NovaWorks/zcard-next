// Local mock browser regression; build admin first. No real products or upstream calls.
const fs = require('node:fs'), path = require('node:path'), http = require('node:http'), assert = require('node:assert/strict');
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const root = process.env.ZCARD_ADMIN_DIST || path.resolve(__dirname, '../admin/dist');
const out = process.env.ZCARD_TEST_OUTPUT || '/tmp/zcard-product-cover-recommend';
fs.mkdirSync(out, { recursive: true });
const server = http.createServer((req, res) => {
  let file = path.join(root, new URL(req.url, 'http://localhost').pathname.replace(/^\/admin\/?/, ''));
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
  res.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'application/javascript', '.css': 'text/css' })[path.extname(file)] || 'application/octet-stream');
  res.end(fs.readFileSync(file));
});
(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) });
  try {
    for (const width of [1440, 390]) {
      const page = await browser.newPage({ viewport: { width, height: 1050 } });
      page.setDefaultTimeout(15000);
      const errors = [], writes = [];
      let failSave = false, permissions = ['*'];
      const baseProduct = { name: '补封面测试商品', category_id: 7, product_kind: 'standard', cover: '', images: ['/uploads/original-detail.png'], price_cents: 1000, factory_price_cents: 200, points_required: 8, status: 1, stock_type: 'card', fulfillment_mode: 'auto', manual_stock: -1, stock_visible: true, dedup: true, is_recommend: true };
      const products = [{ ...baseProduct, id: 1 }, { ...baseProduct, id: 2, name: '锁定商品', is_locked: true, goods_type: 'physical', track_inventory: true, physical_stock: 25, shipping_mode: 'collect', shipping_countries: ['CN'], description: '<p>商品描述样例</p>' }, { ...baseProduct, id: 3, name: '社交服务', product_kind: 'social_service', price_cents: 0, factory_price_cents: 0, points_required: 0 }];
      const asset = { id: 1, name: '封面样例', url: '/uploads/fixture-cover.png', mime: 'image/png', ref_count: 0 };
      page.on('pageerror', e => errors.push(e.message));
      await page.addInitScript(() => { localStorage.setItem('token', JSON.stringify('fixture')); localStorage.setItem('refreshToken', JSON.stringify('fixture')); });
      await page.route('**/*', async route => {
        const req = route.request(), url = new URL(req.url()), p = url.pathname, write = !['GET', 'HEAD'].includes(req.method());
        if (!['127.0.0.1', 'localhost'].includes(url.hostname)) return route.fulfill({ body: '' });
        if (p.startsWith('/uploads/')) return route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="80" height="80"><rect width="80" height="80" fill="#275efe"/></svg>' });
        if (!p.startsWith('/api/v1/')) return route.continue();
        if (write) writes.push({ path: p, method: req.method(), body: req.postDataJSON() });
        if (write && failSave) return route.fulfill({ status: 500, json: { reason: 'FIXTURE_FAILED', message: '模拟保存失败' } });
        let data = { items: [], total: 0, categories: [], skus: [], controls: [], connections: [] };
        if (p.endsWith('/auth/profile')) data = { admin: { id: 1, username: 'fixture' }, permissions };
        if (p.endsWith('/captcha-config')) data = { enabled: false };
        if (p.endsWith('/products')) data = { products, total: products.length };
        const match = p.match(/\/products\/(\d+)$/);
        if (match) {
          data = products.find(product => product.id === Number(match[1]));
          if (write) Object.assign(data, req.postDataJSON());
        }
        if (p.endsWith('/categories') && !p.includes('/media/')) data = { categories: [{ id: 7, name: '原有分类' }] };
        if (p.endsWith('/media')) data = { items: [asset], total: 1 };
        await route.fulfill({ json: data });
      });
      await page.goto(`http://127.0.0.1:${server.address().port}/admin/product`);
      const edit = page.getByRole('button', { name: '编辑', exact: true }).first();
      await edit.waitFor();
      const box = await edit.boundingBox();
      assert(box.x >= 0 && box.x + box.width <= width, `edit action must be visible without horizontal scrolling at ${width}px`);
      await edit.scrollIntoViewIfNeeded();
      await page.screenshot({ path: path.join(out, `list-${width}.png`) });
      const modal = page.locator('.n-modal').filter({ hasText: '编辑商品' });
      await page.getByRole('button', { name: '补封面：补封面测试商品', exact: true }).click();
      await expect(modal.getByText('图片与详情', { exact: true })).toBeVisible();
      await expect(modal.getByText('封面图', { exact: true })).toBeVisible();
      const cover = modal.locator('.n-form-item').filter({ has: page.getByText('封面图', { exact: true }) });
      const selectCover = async () => {
        await cover.getByRole('button', { name: '从素材库选择', exact: true }).click();
        await page.getByRole('button', { name: '选择素材 封面样例', exact: true }).click();
        await page.getByRole('button', { name: /确定（已选 1 张）/ }).click();
        await expect(page.getByText('选择图片', { exact: true })).not.toBeVisible();
      };
      await selectCover();
      await expect(cover.locator('img')).toHaveCount(1);
      const gallery = modal.locator('.n-form-item').filter({ has: page.getByText('详情图集', { exact: true }) });
      await gallery.getByRole('button', { name: '移除图片 1', exact: true }).click();
      await modal.screenshot({ path: path.join(out, `cover-${width}.png`) });
      failSave = true;
      await modal.getByRole('button', { name: '保存', exact: true }).click();
      await expect(page.getByText('模拟保存失败', { exact: true })).toBeVisible();
      await expect(modal).toBeVisible();
      await expect(cover.locator('img')).toHaveCount(1);
      failSave = false;
      await modal.getByRole('button', { name: '保存', exact: true }).click();
      await expect(modal).not.toBeVisible();
      assert.equal(writes.at(-1).body.cover, asset.url);
      assert.equal(writes.at(-1).body.category_id, 7);
      assert.equal(writes.at(-1).body.images_set, true);
      assert.deepEqual(writes.at(-1).body.images, []);
      assert.equal(writes.at(-1).body.is_recommend, true);
      assert(writes.every(write => write.method === 'PUT' && write.path === '/api/v1/admin/products/1'), 'cover edit must not create a product or category');
      await page.getByRole('button', { name: '更换封面：补封面测试商品', exact: true }).click();
      await cover.getByRole('button', { name: '移除图片 1', exact: true }).click();
      await modal.getByRole('button', { name: '保存', exact: true }).click();
      await expect(modal).not.toBeVisible();
      assert.equal(writes.at(-1).body.cover, '');
      for (const [label, value, cents] of [['售价', '12.00', 'price_cents'], ['成本', '3.00', 'factory_price_cents']]) {
        await page.getByTitle(`修改${label}`, { exact: true }).first().click();
        const input = page.getByPlaceholder(label, { exact: true });
        await input.fill(value); await input.press('Enter');
        await expect.poll(() => writes.at(-1).body[cents]).toBe(Math.round(Number(value) * 100));
        assert.equal(writes.at(-1).body.is_recommend, true, `${label} edit must preserve homepage recommendation`);
      }
      await expect(page.getByRole('button', { name: '补封面：锁定商品', exact: true })).toBeDisabled();
      await page.getByRole('button', { name: '查看', exact: true }).click();
      await page.getByText('图片与详情', { exact: true }).click();
      await expect(cover.getByRole('button', { name: '从素材库选择', exact: true })).toBeDisabled();
      await expect(modal.getByRole('button', { name: '保存', exact: true })).toHaveCount(0);
      await modal.getByRole('button', { name: '取消', exact: true }).click();
      await page.getByRole('button', { name: '补封面：社交服务', exact: true }).click();
      await selectCover();
      await page.getByText('价格库存', { exact: true }).click();
      await expect(modal.getByText('社交服务按货源购买规则动态报价；可编辑商品名称、分类、推荐和图片详情。', { exact: true })).toBeVisible();
      await expect(modal.getByText('SKU 多规格', { exact: true })).toHaveCount(0);
      await modal.getByRole('button', { name: '下一步', exact: true }).click();
      await modal.getByRole('button', { name: '保存', exact: true }).click();
      await expect(modal).not.toBeVisible();
      const social = writes.at(-1);
      assert.equal(social.path, '/api/v1/admin/products/3');
      assert.equal(social.body.cover, asset.url);
      assert.equal(social.body.price_cents, 0);
      assert.equal(social.body.is_recommend, true);
      for (const field of ['manual_stock', 'physical_stock', 'direct_content']) assert.equal(Object.hasOwn(social.body, field), false, `social edit must omit ${field}`);
      const copy = page.locator('.n-modal').filter({ hasText: '复制商品' });
      const copyButton = page.getByRole('row').filter({ hasText: '锁定商品' }).getByRole('button', { name: '复制', exact: true });
      await expect(page.getByRole('row').filter({ hasText: '社交服务' }).getByRole('button', { name: '复制', exact: true })).toHaveCount(0);
      const beforeCopy = writes.length;
      await copyButton.click();
      await expect(copy.getByPlaceholder('请输入商品名称')).toHaveValue('锁定商品（副本）');
      await expect(copy.getByText('运费到付', { exact: true })).toBeVisible();
      await copy.getByRole('button', { name: '取消', exact: true }).click();
      await expect(copy).not.toBeVisible();
      assert.equal(writes.length, beforeCopy, 'opening and canceling a copy must not create a product');
      await copyButton.click();
      await copy.getByPlaceholder('请输入商品名称').fill('新的实体商品');
      await copy.getByText('规格与控件', { exact: true }).click();
      await copy.getByRole('button', { name: '创建', exact: true }).click();
      await expect(copy).not.toBeVisible();
      assert.equal(writes.length, beforeCopy + 1);
      const duplicated = writes.at(-1);
      assert.equal(duplicated.method, 'POST');
      assert.equal(duplicated.path, '/api/v1/admin/products');
      for (const [key, value] of Object.entries({
        name: '新的实体商品', copy_from_product_id: 2, category_id: 7, price_cents: 1000, factory_price_cents: 200, status: 0,
        is_recommend: false, goods_type: 'physical', physical_stock: 0, shipping_mode: 'collect',
        shipping_fee_cents: 0, shipping_countries: ['CN'], description: '<p>商品描述样例</p>',
        images: ['/uploads/original-detail.png'],
      })) assert.deepEqual(duplicated.body[key], value, key);
      for (const key of ['id', 'direct_content', 'upstream_source_id', 'is_locked']) assert.equal(Object.hasOwn(duplicated.body, key), false, key);
      const savedCount = writes.length;
      await page.getByRole('button', { name: '新增商品', exact: true }).click();
      await page.getByPlaceholder('请输入商品名称', { exact: true }).fill('未保存的新商品');
      await page.getByText('图片与详情', { exact: true }).click();
      const creating = page.locator('.n-modal').filter({ hasText: '新增商品' });
      await expect(creating.getByRole('button', { name: '保存', exact: true })).toHaveCount(0);
      await expect(creating.getByRole('button', { name: '创建', exact: true })).toHaveCount(0);
      await creating.getByRole('button', { name: '取消', exact: true }).click();
      await page.getByRole('button', { name: '放弃修改', exact: true }).click();
      assert.equal(writes.length, savedCount, 'cancelled creation must not persist a product');
      permissions = ['catalog:read', 'catalog:read_detail', 'catalog:category_read'];
      await page.reload();
      await page.getByText('补封面测试商品', { exact: true }).first().waitFor();
      await expect(page.getByRole('button', { name: /^补封面：|^更换封面：/ })).toHaveCount(0);
      await expect(page.getByRole('button', { name: '编辑', exact: true })).toHaveCount(0);
      await expect(page.getByRole('button', { name: '复制', exact: true })).toHaveCount(0);
      assert.equal(errors.length, 0, errors.join('\n'));
      console.log(JSON.stringify({ width, editVisible: true, coverSavedWithoutRecreation: true, failedSaveRetained: true, recommendationPreserved: true, socialCoverSaved: true, copiedAsUnlisted: true, permissionsAndLocks: true, pageErrors: errors }));
      await page.close();
    }
  } finally { await browser.close(); server.close(); }
})().catch(error => { console.error(error); server.close(); process.exitCode = 1; });
