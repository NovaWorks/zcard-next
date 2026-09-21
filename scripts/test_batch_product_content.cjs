// Local fullstack regression. Requires an isolated installed server and a login JSON file.
// ZCARD_TEST_BASE_URL=http://127.0.0.1:18177 ZCARD_TEST_LOGIN_FILE=/tmp/login.json
// PLAYWRIGHT_MODULE=/path/to/playwright node scripts/test_batch_product_content.cjs
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = process.env.ZCARD_TEST_BASE_URL;
if (!base || !['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname)) throw Error('Use an isolated local test server');
const output = process.env.ZCARD_TEST_OUTPUT || '/tmp/zcard-batch-content-browser';
fs.mkdirSync(output, { recursive: true });
(async () => {
  const authResponse = await fetch(base + '/api/v1/admin/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: fs.readFileSync(process.env.ZCARD_TEST_LOGIN_FILE, 'utf8') });
  assert.equal(authResponse.status, 200);
  const auth = await authResponse.json(), token = auth.token || auth.access_token;
  assert(token);
  const api = async (url, data, method = data ? 'POST' : 'GET') => {
    const r = await fetch(base + '/api/v1/admin' + url, { method, headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` }, body: data === undefined ? undefined : JSON.stringify(data) });
    const body = await r.json(); assert.equal(r.status, 200, JSON.stringify(body)); return body;
  };
  const suffix = Date.now();
  const parent = await api('/categories', { name: `批量测试${suffix}` });
  const child = await api('/categories', { name: `子类目${suffix}`, parent_id: parent.id });
  const ids = [];
  for (let i = 0; i < 25; i++) ids.push((await api('/products', { name: `批量商品${suffix}-${i}`, category_id: i === 24 ? child.id : parent.id, price_cents: 1234, factory_price_cents: 234, points_required: 8, stock_type: 'card', stock_visible: true, status: 0, description: `<p>原介绍${i}</p>` })).id);
  const image = fs.readFileSync(path.join(__dirname, '../server/internal/mods/media/testdata/still.png')).toString('base64');
  const assets = [];
  for (let i = 0; i < 26; i++) assets.push(await api('/media/upload', { name: `批量素材${suffix}-${i}.png`, content_type: 'image/png', data_base64: image }));
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  page.setDefaultTimeout(15000);
  const pageErrors = [];
  page.on('pageerror', e => pageErrors.push(e.message));
  const modal = page.locator('.batch-content-modal');
  const field = label => modal.locator('.n-form-item').filter({ has: page.getByText(label, { exact: true }) });
  async function action(label, option) {
    await field(label).locator('.n-select').click();
    await page.locator('.n-base-select-option:visible').filter({ hasText: new RegExp(`^${option}$`) }).click();
    await page.locator('.n-base-select-option:visible').first().waitFor({ state: 'hidden' });
  }
  const picker = () => page.locator('.n-modal').filter({ has: page.getByPlaceholder('搜索素材名', { exact: true }) });
  async function chooseAsset(name) {
    const search = picker().getByPlaceholder('搜索素材名', { exact: true });
    await search.fill(name); await search.press('Enter');
    await page.getByRole('button', { name: `选择素材 ${name}`, exact: true }).click();
  }
  try {
    await page.addInitScript(value => localStorage.setItem('token', JSON.stringify(value)), token);
    await page.goto(base + '/admin/product');
    await page.getByText(parent.name, { exact: true }).first().click();
    await page.getByRole('button', { name: '批量修改内容', exact: true }).waitFor();
    const order = await page.getByRole('button', { name: '批量下架', exact: true }).evaluate(el => el.parentElement.parentElement.innerText);
    assert(order.indexOf('批量下架') < order.indexOf('批量修改内容'), 'button must follow batch off-shelf');
    assert(await page.getByRole('button', { name: '批量下架', exact: true }).isDisabled(), 'empty checked selection must not enable off-shelf');
    await page.getByRole('button', { name: '批量修改内容', exact: true }).click();
    await modal.getByRole('button', { name: '下一步', exact: true }).click();
    await action('商品封面', '替换');
    await field('商品封面').getByRole('button', { name: '从素材库选择', exact: true }).click();
    await chooseAsset(assets[25].name);
    await picker().getByRole('button', { name: /确定（已选 1 张）/ }).click();
    await action('详情图集', '替换');
    await field('详情图集').getByRole('button', { name: '从素材库选择', exact: true }).click();
    // Cross-page selection: filter this fixture, choose page one then page two.
    await picker().getByPlaceholder('搜索素材名').fill(`批量素材${suffix}-`);
    await picker().getByPlaceholder('搜索素材名').press('Enter');
    await page.getByRole('button', { name: `选择素材 ${assets[25].name}`, exact: true }).click();
    await picker().getByTitle('最后一页', { exact: true }).click();
    await page.getByRole('button', { name: `选择素材 ${assets[0].name}`, exact: true }).click();
    await picker().getByRole('button', { name: /确定（已选 2 张）/ }).click();
    assert.equal(await field('详情图集').locator('img').count(), 2, 'cross-page picks lost');
    await field('详情图集').getByRole('button', { name: '后移图片 1', exact: true }).click();
    // Reopen keeps both choices even when one is off the first page, then cancel.
    await field('详情图集').getByRole('button', { name: '从素材库选择', exact: true }).click();
    await picker().getByRole('button', { name: /确定（已选 2 张）/ }).waitFor();
    await picker().getByRole('button', { name: '取消', exact: true }).click();
    await action('产品介绍', '替换');
    await modal.locator('[contenteditable=true]').fill('统一产品介绍');
    await modal.getByRole('button', { name: '生成预览', exact: true }).click();
    await modal.getByRole('button', { name: '确认修改', exact: true }).waitFor();
    assert.match(await modal.innerText(), /共 25 件/);
    await modal.getByText('预览商品：', { exact: false }).waitFor();
    assert(!(await modal.innerText()).includes('NaN'), 'missing protobuf defaults');
    await page.screenshot({ path: path.join(output, 'desktop-preview.png'), fullPage: true, animations: 'disabled' });
    await modal.getByRole('button', { name: '确认修改', exact: true }).click();
    await modal.waitFor({ state: 'hidden' });
    for (const id of ids) {
      const p = await api(`/products/${id}`);
      assert.equal(p.cover, assets[25].url); assert.deepEqual(p.images, [assets[0].url, assets[25].url]);
      assert(p.description.includes('统一产品介绍')); assert.equal(p.price_cents, 1234); assert.equal(p.factory_price_cents, 234); assert.equal(p.points_required, 8); assert.equal(p.status || 0, 0);
    }
    console.log('PASS desktop: category descendants, cross-page media, ordering, preview, atomic save');
    await api('/products/batch-status', { ids: [ids[0]], status: 1 });
    const storefront = await browser.newPage();
    await storefront.goto(`${base}/product/${ids[0]}`);
    await storefront.getByText('统一产品介绍', { exact: true }).waitFor();
    assert(await storefront.locator(`img[src$="${assets[25].url}"]`).count() > 0, 'storefront cover missing');
    await storefront.screenshot({ path: path.join(output, 'storefront-detail.png'), fullPage: true, animations: 'disabled' });
    await storefront.close();
    await api('/products/batch-status', { ids: [ids[0]], status: 0 });
    console.log('PASS storefront: updated cover and description rendered');
    // Mobile: complete a scoped clear and verify no modal overflows.
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByRole('button', { name: '批量修改内容', exact: true }).click();
    await modal.getByRole('button', { name: '下一步', exact: true }).click();
    await action('商品封面', '替换');
    await field('商品封面').getByRole('button', { name: '从素材库选择', exact: true }).click();
    await picker().getByPlaceholder('搜索素材名').waitFor();
    await picker().getByRole('button', { name: '取消', exact: true }).hover();
    const box = await picker().boundingBox(); assert(box.x >= 0 && box.x + box.width <= 391, 'mobile picker overflow');
    assert(await picker().evaluate(el => el.scrollWidth <= el.clientWidth + 1), 'mobile picker content overflow');
    await page.screenshot({ path: path.join(output, 'mobile-media-picker.png'), fullPage: true, animations: 'disabled' });
    await picker().getByRole('button', { name: '取消', exact: true }).click();
    await action('商品封面', '保持不变');
    await action('产品介绍', '清空');
    await modal.getByRole('button', { name: '生成预览', exact: true }).click();
    await modal.getByRole('button', { name: '确认修改', exact: true }).waitFor();
    assert.match(await modal.innerText(), /介绍：清空/);
    assert.match(await modal.innerText(), /封面：保持不变/);
    const mbox = await modal.boundingBox(); assert(mbox.x >= 0 && mbox.x + mbox.width <= 391, 'mobile form overflow');
    await page.screenshot({ path: path.join(output, 'mobile-preview.png'), fullPage: true, animations: 'disabled' });
    await modal.getByRole('button', { name: '确认修改', exact: true }).click();
    await modal.waitFor({ state: 'hidden' });
    assert.equal((await api(`/products/${ids[0]}`)).description || '', '');
    assert.equal((await api(`/products/${ids[0]}`)).cover, assets[25].url);
    assert.deepEqual(pageErrors, []);
    console.log('PASS mobile: media picker, no modal overflow, explicit clear preserves images');
  } catch (e) {
    await page.screenshot({ path: path.join(output, 'failure.png'), fullPage: true, animations: 'disabled' });
    throw e;
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exit(1); });
