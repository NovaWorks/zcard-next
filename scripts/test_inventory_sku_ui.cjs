/** Production-build SKU inventory UI regression with disposable local API fixtures.
 * Build admin and storefront first. Set PLAYWRIGHT_MODULE and CHROME_PATH if needed.
 */
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const moduleName = process.env.PLAYWRIGHT_MODULE || 'playwright';
const automation = require(moduleName);
const { chromium } = automation;
const { expect } = automation.expect ? automation : require(path.join(path.dirname(require.resolve(moduleName + '/package.json')), 'test.js'));
const roots = { admin: path.resolve(__dirname, '../admin/dist'), storefront: path.resolve(__dirname, '../storefront/dist') };
const out = process.env.ZCARD_TEST_OUTPUT || fs.mkdtempSync(path.join(os.tmpdir(), 'zcard-inventory-sku-ui-'));
const entries = Object.entries({ 'site.name': '规格库存验收', 'trade.cart_enabled': true, 'security.captcha_order': false, 'i18n.default_locale': 'zh_CN' }).map(([key, value]) => ({ key, value_json: JSON.stringify(value) }));
const products = [{ id: 10, name: '规格卡密商品', category_id: 1, stock_type: 'card', price_cents: 1000 }, { id: 20, name: '无规格商品', category_id: 1, stock_type: 'card', price_cents: 1000 }];
let state;
function reset() {
  state = { permissions: ['*'], requests: [], failTarget20: 0, failCancel: true, releaseSlow: null, cards: [
    { id: 1, product_id: 10, status: 'available', import_id: 7, masked_content: '****0001' },
    { id: 2, product_id: 10, sku_id: 101, sku_name: '月卡', status: 'available', import_id: 7, masked_content: '****0002' },
    { id: 3, product_id: 10, status: 'reserved', import_id: 7, masked_content: '****0003' },
    { id: 4, product_id: 10, status: 'used', import_id: 7, masked_content: '****0004' },
  ], batchStatus: 'done' };
}
function target(product) {
  return { product_id: product, product_name: product === 10 ? '规格卡密商品' : '无规格商品', revision: 4, has_skus: product === 10,
    targets: product === 10 ? [{ sku_name: '商品默认池（未分配）', can_import: false, reason: '请选择具体规格', available: 1, reserved: 1 },
      { sku_id: 101, sku_name: '月卡', can_import: true, available: 1 }, { sku_id: 102, sku_name: '年卡', can_import: true }]
      : [{ sku_name: '商品默认池', can_import: true }] };
}
function distribution() {
  const result = new Map();
  for (const card of state.cards.filter(c => c.import_id === 7)) {
    const id = card.sku_id || 0;
    if (!result.has(id)) result.set(id, { sku_id: id, sku_name: id === 101 ? '月卡' : id === 102 ? '年卡' : '商品默认池' });
    const group = result.get(id); group[card.status] = (group[card.status] || 0) + 1;
  }
  return [...result.values()];
}
function batch() { return { id: 7, product_id: 10, product_name: '规格卡密商品', total: 4, imported: 4, status: state.batchStatus, remaining: distribution() }; }
function storefrontProduct() { return { id: 10, name: '规格卡密商品', slug: 'sku-card', stock_type: 'card', stock: 20, stock_visible: true, price_cents: 1000, points_required: 100, controls: [], description: '<p>规格库存测试</p>', skus: [{ id: 101, name: '月卡', stock: 10, price_cents: 1000 }, { id: 102, name: '年卡', stock: 10, price_cents: 2000 }] }; }
const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://local'), p = url.pathname;
  res.setHeader('Cache-Control', 'no-store');
  if (p.startsWith('/api/')) {
    const chunks = []; for await (const c of req) chunks.push(c);
    const body = chunks.length ? JSON.parse(Buffer.concat(chunks)) : undefined;
    state.requests.push({ p, method: req.method, query: Object.fromEntries(url.searchParams), body });
    res.setHeader('Content-Type', 'application/json');
    let data = { items: [], products: [], categories: [], currencies: [], total: 0 };
    if (p.endsWith('/auth/profile')) data = { admin: { id: 1, username: 'fixture' }, permissions: state.permissions };
    else if (p.endsWith('/admin/products')) data = { products, total: products.length };
    else if (p.endsWith('/categories')) data = { categories: [{ id: 1, name: '测试分类' }] };
    else if (p.endsWith('/inventory/targets')) {
      const id = Number(url.searchParams.get('product_id'));
      if (id === 20 && state.failTarget20-- > 0) { res.statusCode = 503; data = { reason: 'inventory.TARGET_FAILED', message: '目标规格加载失败' }; }
      else data = target(id);
    } else if (p.endsWith('/inventory/cards')) {
      const wanted = state.cards.filter(c => c.product_id === Number(url.searchParams.get('product_id')) && (!url.searchParams.has('sku_id') || (c.sku_id || 0) === Number(url.searchParams.get('sku_id'))));
      data = { cards: wanted, total: wanted.length };
    } else if (p.endsWith('/inventory/cards/premium')) data = { cards: [], total: 0 };
    else if (p.endsWith('/inventory/imports')) data = { batches: [batch()] };
    else if (p.endsWith('/inventory/preview')) {
      if (body.lines[0] === 'slow-old') await new Promise(resolve => { state.releaseSlow = resolve; });
      data = { total: body.lines[0] === 'slow-old' ? 111 : body.lines.length, sku_id: body.sku_id, sku_name: body.sku_id === 101 ? '月卡' : body.sku_id === 102 ? '年卡' : '商品默认池', revision: 4 };
    } else if (p.endsWith('/inventory/import')) {
      await new Promise(resolve => setTimeout(resolve, 150)); data = { id: 8, product_id: body.product_id, sku_id: body.sku_id, imported: body.lines.length, status: 'done' };
    } else if (p.endsWith('/inventory/export')) data = { lines: ['fixture-export'] };
    else if (p.endsWith('/inventory/assign-preview')) {
      data = { card_ids: [1], eligible_count: 1, excluded_count: body.import_id ? 3 : 0, revision: 4, sku_name: body.sku_id === 101 ? '月卡' : '年卡' };
    } else if (p.endsWith('/inventory/assign')) {
      const card = state.cards.find(c => c.id === 1); card.sku_id = body.sku_id; card.sku_name = body.sku_id === 101 ? '月卡' : '年卡'; data = { assigned: body.card_ids.length };
    } else if (p.endsWith('/inventory/imports/7/preview')) data = { batch: batch(), revision: 'fixture-revision' };
    else if (p.endsWith('/inventory/imports/7/cancel')) {
      if (state.failCancel) { state.failCancel = false; res.statusCode = 409; data = { reason: 'inventory.CONFLICT', message: '库存已变化' }; }
      else { state.cards = state.cards.filter(c => c.status !== 'available'); state.batchStatus = 'canceled'; data = {}; }
    } else if (p.endsWith('/config')) data = { entries };
    else if (p.endsWith('/storefront/products')) data = { items: [{ ...storefrontProduct(), skus: undefined }], total: 1 };
    else if (p.endsWith('/storefront/products/10')) data = storefrontProduct();
    else if (p.endsWith('/member-level/me')) data = { points: 1000 };
    else if (p.endsWith('/storefront/user/profile')) data = { user: { id: 1, username: 'fixture' } };
    return res.end(JSON.stringify(data));
  }
  const admin = p.startsWith('/admin/'), root = roots[admin ? 'admin' : 'storefront'];
  let file = path.join(root, admin ? p.slice(7) : p);
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
  res.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.png': 'image/png', '.svg': 'image/svg+xml' })[path.extname(file)] || 'application/octet-stream');
  let content = fs.readFileSync(file);
  if (!admin && path.extname(file) === '.html') content = content.toString().replace('<head>', '<head><script id="zcard-theme-runtime" type="application/json">' + JSON.stringify({ key: 'classic', branding: { name: '规格库存验收', logo: '' }, values: {}, capabilities: {}, public_config: { entries } }) + '</script>');
  res.end(content);
});
async function choose(page, label, text) {
  const select = page.locator(`[aria-label="${label}"]`);
  await expect.poll(() => select.locator('.n-base-selection--disabled').count()).toBe(0);
  await select.click();
  await page.locator('.n-base-select-option:visible').filter({ hasText: text }).last().click();
}
async function latest(page, suffix, action) {
  const promise = page.waitForResponse(r => r.url().split('?')[0].endsWith(suffix));
  await action(); await promise;
}
(async () => {
  reset(); await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = 'http://127.0.0.1:' + server.address().port;
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) });
  try {
    for (const width of [1440, 390]) {
      reset(); const page = await browser.newPage({ viewport: { width, height: 1000 } }); const errors = [];
      page.on('pageerror', e => errors.push(e.message));
      await page.addInitScript(() => { localStorage.setItem('token', JSON.stringify('fixture')); localStorage.setItem('refreshToken', JSON.stringify('fixture')); localStorage.setItem('zcard_token', 'fixture'); });
      await page.goto(base + '/admin/inventory?product_id=10&open_import=1');
      const modal = page.locator('.n-modal').filter({ hasText: '按规格导入卡密' });
      await expect(modal).toBeVisible(); await expect(modal.getByRole('button', { name: '确认导入', exact: true })).toBeDisabled();
      await choose(page, '导入规格', '月卡');
      const oldRequest = page.waitForRequest(r => r.url().endsWith('/inventory/preview') && r.postDataJSON().lines[0] === 'slow-old'); oldRequest.catch(() => {});
      await modal.getByLabel('导入卡密内容').fill('slow-old'); await oldRequest;
      await choose(page, '导入规格', '年卡'); await modal.getByLabel('导入卡密内容').fill('annual-1\nannual-2\nannual-3');
      await expect(modal).toContainText('总计 3 条'); state.releaseSlow();
      await expect(modal).toContainText('总计 3 条'); await expect(modal).not.toContainText('111');
      await latest(page, '/inventory/import', () => modal.getByRole('button', { name: '确认导入', exact: true }).click());
      await expect(modal).toBeHidden();
      const submitted = state.requests.find(r => r.p.endsWith('/inventory/import')).body;
      assert.equal(submitted.product_id, 10); assert.equal(submitted.sku_id, 102); assert.equal(submitted.expected_revision, 4); assert.equal(submitted.lines.length, 3);
      await choose(page, '库存规格', '商品默认池');
      await expect.poll(() => state.requests.filter(r => r.p.endsWith('/inventory/cards')).at(-1)?.query.sku_id).toBe('0');
      await choose(page, '库存规格', '月卡');
      await latest(page, '/inventory/export', () => page.getByRole('button', { name: '导出当前规格', exact: true }).click());
      assert.equal(state.requests.filter(r => r.p.endsWith('/inventory/export')).at(-1).body.sku_id, 101);
      await page.locator('.n-tabs-tab').filter({ hasText: '导入批次' }).click();
      await page.getByRole('button', { name: '分配默认池', exact: true }).first().click();
      const assign = page.locator('.n-modal').filter({ hasText: '分配默认池卡密到规格' }); await expect(assign).toBeVisible();
      await choose(page, '归属目标规格', '年卡');
      await assign.getByRole('button', { name: '预览归属', exact: true }).click(); await expect(assign).toContainText('将分配 1 张');
      await choose(page, '归属目标规格', '月卡'); await expect(assign.getByRole('button', { name: '确认分配', exact: true })).toBeDisabled();
      await choose(page, '归属目标规格', '年卡'); await assign.getByRole('button', { name: '预览归属', exact: true }).click();
      await expect(assign.getByRole('button', { name: '确认分配', exact: true })).toBeEnabled();
      await assign.getByRole('button', { name: '确认分配', exact: true }).click(); await expect(assign).toBeHidden();
      const assignment = state.requests.filter(r => r.p.endsWith('/inventory/assign')).at(-1).body;
      assert.deepEqual(assignment.card_ids, [1]); assert.equal(assignment.sku_id, 102); assert.equal(state.cards[0].import_id, 7);
      await page.getByRole('button', { name: '撤销批次', exact: true }).click();
      const cancel = page.locator('.n-modal').filter({ hasText: '撤销原始导入批次' }); await expect(cancel).toContainText('所有当前规格');
      await expect(cancel).toContainText('年卡：可用 1');
      await cancel.getByRole('button', { name: '确认撤销整批可用卡', exact: true }).click(); await expect(cancel).toContainText('批次库存可能已变化');
      await expect(cancel.getByRole('button', { name: '确认撤销整批可用卡', exact: true })).toBeDisabled();
      await cancel.getByRole('button', { name: '重新预览', exact: true }).click(); await expect(cancel.getByRole('button', { name: '确认撤销整批可用卡', exact: true })).toBeEnabled();
      await cancel.getByRole('button', { name: '确认撤销整批可用卡', exact: true }).click(); await expect(cancel).toBeHidden();
      assert.equal(state.requests.filter(r => r.p.endsWith('/imports/7/cancel')).at(-1).body.expected_revision, 'fixture-revision');
      assert(state.cards.some(c => c.status === 'reserved') && state.cards.some(c => c.status === 'used'));
      state.failTarget20 = 1;
      await page.getByRole('button', { name: '导入卡密', exact: true }).click();
      await choose(page, '导入商品', '无规格商品');
      await expect(modal).toContainText('规格加载失败'); await expect(modal.getByRole('button', { name: '确认导入', exact: true })).toBeDisabled();
      await modal.getByRole('button', { name: '重试', exact: true }).click(); await modal.getByLabel('导入卡密内容').fill('default-card');
      await expect(modal).toContainText('商品默认池'); await expect(modal.getByRole('button', { name: '确认导入', exact: true })).toBeEnabled();
      const box = await modal.boundingBox(); assert(box.x >= 0 && box.x + box.width <= width, 'import dialog overflows');
      await page.screenshot({ path: path.join(out, `inventory-${width}.png`), fullPage: true });
      await modal.getByRole('button', { name: '取消', exact: true }).click(); await expect(modal).toBeHidden();
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'inventory page overflows');
      assert.deepEqual(errors, []); await page.close(); console.log(`SKU inventory ${width}px: passed`);
    }
    reset(); state.permissions = ['inventory:read'];
    const reader = await browser.newPage();
    await reader.addInitScript(() => { localStorage.setItem('token', JSON.stringify('fixture')); localStorage.setItem('refreshToken', JSON.stringify('fixture')); });
    await reader.goto(base + '/admin/inventory?product_id=10');
    await expect(reader.getByLabel('库存商品 ID', { exact: true })).toBeVisible(); await expect(reader.getByText('规格卡密商品 · 默认池', { exact: false })).toBeVisible();
    assert(!state.requests.some(r => r.p.endsWith('/admin/products')), 'inventory reader must not need catalog permission');
    await expect(reader.getByRole('button', { name: '导入卡密', exact: true })).toHaveCount(0); await reader.close();
    console.log('Inventory reader permissions: passed');
    reset(); const unsafe = await browser.newPage();
    await unsafe.addInitScript(() => { localStorage.setItem('token', JSON.stringify('fixture')); localStorage.setItem('refreshToken', JSON.stringify('fixture')); });
    await unsafe.goto(base + '/admin/inventory?product_id=9007199254740993');
    await expect(unsafe.getByText('商品标识无效或超出安全范围', { exact: true })).toBeVisible();
    assert(!state.requests.some(r => r.p.endsWith('/inventory/targets')), 'unsafe product ID must not be rounded and sent');
    await unsafe.close(); console.log('Unsafe route ID rejection: passed');

    reset(); const points = await browser.newPage();
    await points.addInitScript(() => localStorage.setItem('zcard_token', 'fixture'));
    await points.goto(base + '/points');
    await points.getByRole('button', { name: '积分兑换', exact: true }).click();
    await expect(points).toHaveURL(/\/product\/10$/); await expect(points.getByText('选择规格', { exact: false })).toBeVisible();
    assert(!state.requests.some(r => r.p.endsWith('/orders') && r.method === 'POST'), 'points list must not submit an order without SKU');
    await points.close(); console.log('Points SKU selection: passed');
    console.log('Screenshots:', out);
  } finally { if (state.releaseSlow) state.releaseSlow(); await browser.close(); await new Promise(resolve => server.close(resolve)); }
})().catch(error => { console.error(error); if (state.releaseSlow) state.releaseSlow(); server.close(); process.exitCode = 1; });
