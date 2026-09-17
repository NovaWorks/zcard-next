/** Regression against a production admin build and disposable API fixtures.
 * pnpm --dir admin build
 * PLAYWRIGHT_MODULE=/path/to/@playwright/test node scripts/test_admin_navigation.cjs
 * ADMIN_DIST may point at the exact embedded release assets.
 */
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const assert = require('node:assert/strict');
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const root = process.env.ADMIN_DIST || path.resolve(__dirname, '../admin/dist');
const out = process.env.ZCARD_TEST_OUTPUT || '/tmp/zcard-admin-navigation';
fs.mkdirSync(out, { recursive: true });
const server = http.createServer((req, res) => {
  let file = path.join(root, new URL(req.url, 'http://localhost').pathname.replace(/^\/admin\/?/, ''));
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
  res.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'application/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png' })[path.extname(file)] || 'application/octet-stream');
  res.end(fs.readFileSync(file));
});

(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(() => localStorage.setItem('token', JSON.stringify('local-navigation-fixture')));
  await page.route('**/api/v1/**', async route => {
    const pathname = new URL(route.request().url()).pathname;
    let data = { items: [], total: 0, entries: [], categories: [], currencies: [], orders: [], users: [], products: [], admins: [], logs: [], roles: [], templates: [], trend: [], points: [], top_products: [], top_channels: [], pending: {} };
    if (pathname.endsWith('/auth/profile')) data = { admin: { id: 1, username: 'local-test' }, permissions: ['*'] };
    await route.fulfill({ json: data });
  });
  const main = page.locator('main');
  async function navigate(label, expected = label) {
    await page.getByRole('menuitem', { name: label, exact: true }).click();
    await expect(main).toContainText(expected);
    // Wait for the outgoing animation to finish before the next real menu click.
    await expect.poll(() => main.evaluate(el => [...el.querySelectorAll('*')].some(node => /(?:enter|leave)-active/.test(node.className || '')))).toBe(false);
  }
  try {
    await page.goto(base + '/admin/user');
    await expect(main).toContainText('用户管理');
    // Leaving staff used to strand Transition in isLeaving=true; all later
    // routes became blank despite successful API responses and changed URLs.
    for (const label of ['用户管理', '商品管理', '订单管理', '系统设置', '内容管理', '首页']) {
      await navigate('员工管理');
      await navigate(label, label === '首页' ? '工作台' : label);
      console.log('PASS staff →', label);
    }
    await navigate('员工管理');
    await navigate('系统设置');
    await page.goBack();
    await expect(main).toContainText('员工管理');
    await page.goForward();
    await expect(main).toContainText('系统设置');
    await page.reload();
    await expect(main).toContainText('系统设置');
    await navigate('员工管理');
    await navigate('用户管理');
    await page.screenshot({ path: path.join(out, 'navigation-desktop.png') });
    assert.deepEqual(errors, [], errors.join('\n'));
    console.log('PASS repeated menu navigation, browser back/forward, reload, no runtime errors');
  } catch (error) {
    await page.screenshot({ path: path.join(out, 'failure.png') });
    console.error('Browser errors:', errors);
    throw error;
  } finally {
    await browser.close();
    server.close();
  }
})().catch(error => { console.error(error); server.close(); process.exitCode = 1; });
