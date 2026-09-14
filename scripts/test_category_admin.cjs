/** Local fullstack category regression. Uses an isolated, disposable database.
 * ZCARD_TEST_BASE_URL=http://127.0.0.1:18149 ZCARD_TEST_LOGIN_FILE=/path/login.json
 * PLAYWRIGHT_MODULE=/path/to/playwright node scripts/test_category_admin.cjs
 * login.json: {"username":"test-admin","password":"..."}; never commit credentials.
 */
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = process.env.ZCARD_TEST_BASE_URL;
if (!base || !['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname)) throw Error('Use an isolated local test server');
const out = process.env.ZCARD_TEST_OUTPUT || '/tmp/zcard-category-browser';
fs.mkdirSync(out, { recursive: true });
(async () => {
  const login = await fetch(base + '/api/v1/admin/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: fs.readFileSync(process.env.ZCARD_TEST_LOGIN_FILE, 'utf8') });
  assert.equal(login.status, 200);
  const auth = await login.json();
  const token = auth.token || auth.access_token;
  assert(token, 'login returned a token');
  const api = async (url, method = 'GET', data) => {
    const r = await fetch(base + '/api/v1' + url, { method, headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` }, body: data === undefined ? undefined : JSON.stringify(data) });
    const body = await r.json();
    assert.equal(r.status, 200, JSON.stringify(body));
    return body;
  };
  const suffix = Date.now();
  const parentName = `邮箱测试${suffix}`;
  const emojiName = `游戏测试${suffix}`;
  const parent = await api('/admin/categories', 'POST', { name: parentName });
  await api('/admin/categories', 'POST', { name: emojiName, icon: '🎮' });
  const brokenName = `失效图片${suffix}`;
  await api('/admin/categories', 'POST', { name: brokenName, icon: '/uploads/missing-category-icon.png' });
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  page.setDefaultTimeout(15000);
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  const row = name => page.locator('.category-manage-row').filter({ has: page.locator('.category-full-name', { hasText: name }) });
  const expectImage = async locator => {
    await locator.waitFor();
    await page.waitForFunction(el => el.complete && el.naturalWidth > 0, await locator.elementHandle());
  };
  try {
    await page.addInitScript(value => localStorage.setItem('token', JSON.stringify(value)), token);
    await page.goto(base + '/admin/category');
    await row(parentName).waitFor();
    console.log('category loaded');
    assert(await page.getByText('商品分类', { exact: true }).count() >= 2, 'standalone menu and page title');
    // Upload and select a real image through the shared media picker.
    await row(parentName).getByTitle('设置图标').click();
    await page.getByRole('button', { name: '从素材库选择', exact: true }).click();
    const upload = page.waitForResponse(r => r.url().endsWith('/admin/media/upload') && r.request().method() === 'POST');
    await page.locator('input[type=file]').setInputFiles(path.join(__dirname, '../server/internal/mods/media/testdata/still.png'));
    const uploaded = await (await upload).json();
    assert(uploaded.url, 'upload returned image URL');
    console.log('uploaded');
    await page.locator(`img[src="${uploaded.url}"]`).first().click();
    await page.getByRole('button', { name: /确定（已选 1 张）/ }).click();
    await expectImage(row(parentName).locator('.category-icon img'));
    console.log('image visible');
    assert.equal((await api('/admin/categories')).categories.find(c => c.id === parent.id).icon, uploaded.url);
    await page.getByRole('button', { name: '刷新', exact: true }).click();
    await expectImage(row(parentName).locator('.category-icon img'));
    // Full-path search, empty state, and quick child creation.
    await page.getByPlaceholder('搜索分类名称或完整路径', { exact: true }).fill('no-match-category');
    await page.getByText('未找到分类，请换个关键词或清空搜索', { exact: true }).waitFor();
    await page.getByPlaceholder('搜索分类名称或完整路径', { exact: true }).fill('');
    await row(parentName).getByRole('button', { name: parentName + '操作', exact: true }).click();
    await page.getByText('添加子分类', { exact: true }).click();
    const childName = `企业邮箱${suffix}`;
    await page.getByPlaceholder('分类名称', { exact: true }).fill(childName);
    await page.getByRole('button', { name: '创建', exact: true }).click();
    await row(childName).waitFor();
    console.log('child created');
    const child = (await api('/admin/categories')).categories.find(c => c.name === childName);
    assert.equal(child.parent_id, parent.id);
    await page.getByPlaceholder('搜索分类名称或完整路径', { exact: true }).fill(parentName + ' / ' + childName);
    assert.equal(await page.locator('.category-manage-row').count(), 1);
    assert.equal(await row(childName).getAttribute('draggable'), 'false');
    await page.getByPlaceholder('搜索分类名称或完整路径', { exact: true }).fill('');
    // Parent selector blocks cycles and can move a child back to root.
    await row(parentName).getByRole('button', { name: parentName + '操作', exact: true }).click();
    await page.getByText('调整上级分类', { exact: true }).click();
    let move = page.locator('.n-modal').filter({ has: page.getByText('调整上级分类', { exact: true }) });
    await move.locator('.n-select').click();
    await move.locator('.n-select input').fill(parentName);
    await page.locator('.n-base-select-option--disabled').first().waitFor();
    assert.equal(await page.locator('.n-base-select-option--disabled').count(), 2);
    await page.keyboard.press('Escape');
    await move.getByRole('button', { name: '取消', exact: true }).click();
    await row(childName).getByRole('button', { name: childName + '操作', exact: true }).click();
    await page.getByText('调整上级分类', { exact: true }).click();
    await move.locator('.n-select').click();
    await move.locator('.n-select input').fill('顶级分类');
    await page.locator('.n-base-select-option').filter({ hasText: '顶级分类' }).click();
    await page.route(`**/admin/categories/${child.id}`, route => route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ code: 400, message: '测试保存失败，请重试' }) }), { times: 1 });
    await move.getByRole('button', { name: '保存', exact: true }).click();
    await page.getByText('测试保存失败，请重试', { exact: true }).waitFor();
    assert.equal((await api('/admin/categories')).categories.find(c => c.id === child.id).parent_id, parent.id);
    assert(await move.isVisible(), 'failed save keeps the form open');
    await move.getByRole('button', { name: '保存', exact: true }).click();
    await move.waitFor({ state: 'hidden' });
    assert.equal((await api('/admin/categories')).categories.find(c => c.id === child.id).parent_id || 0, 0);
    // Changing an icon updates the same page immediately and survives reloading.
    await row(emojiName).getByTitle('更换图标').click();
    await page.getByRole('button', { name: '📧', exact: true }).click();
    await page.waitForFunction(name => [...document.querySelectorAll('.category-manage-row')].find(el => el.textContent.includes(name))?.querySelector('.category-icon')?.textContent.trim() === '📧', emojiName);
    console.log('parent and emoji changes passed');
    await page.reload();
    await row(parentName).waitFor();
    await expectImage(row(parentName).locator('.category-icon img'));
    await page.screenshot({ path: path.join(out, 'category-desktop.png') });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForFunction(() => document.querySelector('.category-manager').getBoundingClientRect().left < 50);
    await page.waitForFunction(() => document.documentElement.scrollWidth <= innerWidth);
    await page.screenshot({ path: path.join(out, 'category-mobile.png') });
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'no page overflow on mobile');
    await row(childName).getByRole('button', { name: childName + '操作', exact: true }).click();
    await page.getByText('重命名', { exact: true }).click();
    await page.getByText('分类重命名', { exact: true }).waitFor();
    await page.keyboard.press('Escape');
    await page.setViewportSize({ width: 1440, height: 1000 });
    // Actual menu navigation, legacy product dialog, image and fallback tree nodes.
    await page.getByRole('menuitem', { name: '商品管理', exact: true }).click();
    await page.waitForURL('**/admin/product');
    const tree = page.locator('.product-category-card');
    await expectImage(tree.locator('.n-tree-node').filter({ hasText: parentName }).locator('.category-icon img'));
    assert.equal(await tree.locator('.n-tree-node').filter({ hasText: emojiName }).locator('.category-icon').innerText(), '📧');
    await page.waitForFunction(name => [...document.querySelectorAll('.n-tree-node')].find(el => el.textContent.includes(name))?.querySelector('.category-icon')?.textContent.trim() === '🏷️', brokenName);
    await tree.getByRole('button', { name: '管理', exact: true }).click();
    await expectImage(row(parentName).locator('.category-icon img'));
    await page.keyboard.press('Escape');
    await page.screenshot({ path: path.join(out, 'product-icons.png') });
    await page.getByRole('menuitem', { name: '商品分类', exact: true }).click();
    await row(parentName).waitFor();
    await expectImage(row(parentName).locator('.category-icon img'));
    // A relative legacy upload path uses the site root instead of /admin/.
    await api('/admin/categories/' + parent.id, 'PUT', { icon: uploaded.url.slice(1) });
    await page.getByRole('button', { name: '刷新', exact: true }).click();
    await expectImage(row(parentName).locator('.category-icon img'));
    await api('/admin/categories/' + (await api('/admin/categories')).categories.find(c => c.name === brokenName).id, 'PUT', { icon: uploaded.url });
    await page.getByRole('button', { name: '刷新', exact: true }).click();
    await expectImage(row(brokenName).locator('.category-icon img'));
    await page.goto(base + '/');
    await expectImage(page.locator(`img[src="${uploaded.url}"]`).first());
    await page.screenshot({ path: path.join(out, 'storefront-icons.png') });
    assert.deepEqual(errors, [], 'no browser JavaScript exceptions');
    // Read-only category permissions do not expose mutation controls.
    await page.route('**/admin/auth/profile', async route => {
      const response = await route.fetch(); const body = await response.json(); body.permissions = ['catalog:category_read'];
      await route.fulfill({ response, json: body });
    });
    await page.goto(base + '/admin/category');
    await row(parentName).waitFor();
    assert.equal(await page.getByRole('button', { name: '新建分类', exact: true }).count(), 0);
    assert.equal(await row(parentName).getByRole('button').count(), 0);
    assert.equal(await row(parentName).getAttribute('draggable'), 'false');
    assert.equal(await row(parentName).locator('input').isDisabled(), true);
    assert.deepEqual(errors, [], 'no JavaScript exceptions after read-only navigation');
    console.log('PASS: upload/select/save; image persistence and legacy paths; standalone menu and reload; search/empty state; child creation; cycle prevention and reparenting; immediate emoji refresh; desktop/mobile; old modal; product fallback; storefront; read-only UI; no JS errors.');
  } catch (error) {
    await page.screenshot({ path: path.join(out, 'failure.png') });
    console.error('URL:', page.url(), 'JS errors:', errors);
    throw error;
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
