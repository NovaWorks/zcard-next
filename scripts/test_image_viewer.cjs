/** Production bundle browser checks with disposable product fixtures.
 * PLAYWRIGHT_MODULE=/path/to/playwright/test node scripts/test_image_viewer.cjs
 */
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const { createServer } = require('node:http');
const fs = require('node:fs'), path = require('node:path'), assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const out = fs.mkdtempSync(path.join(require('node:os').tmpdir(), 'zcard-images-'));
const dist = path.join(out, 'dist');
const build = spawnSync('pnpm', ['exec', 'vite', 'build', '--outDir', dist], { cwd: path.resolve(__dirname, '../storefront'), encoding: 'utf8' });
assert.equal(build.status, 0, build.stderr);
const server = createServer((req, res) => {
  const p = new URL(req.url, 'http://localhost').pathname;
  if (p.endsWith('.svg')) {
    res.setHeader('Content-Type', 'image/svg+xml');
    return res.end('<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="1800"><rect width="1200" height="1800" fill="#134e4a"/><text x="100" y="300" fill="white" font-size="100">Product image</text></svg>');
  }
  let file = path.join(dist, p);
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(dist, 'index.html');
  res.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'application/javascript', '.css': 'text/css' })[path.extname(file)] || 'application/octet-stream');
  res.end(fs.readFileSync(file));
});
(async () => {
  await new Promise(r => server.listen(0, '127.0.0.1', r));
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) });
  try {
    for (const fallback of [false, true]) for (const width of [1280, 390]) {
      const page = await browser.newPage({ viewport: { width, height: 850 } });
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));
      if (fallback) await page.addInitScript(() => {
        HTMLDialogElement.prototype.showModal = undefined;
        HTMLDialogElement.prototype.close = undefined;
        window.ResizeObserver = undefined;
      });
      await page.route('**/api/**', route => {
        const p = new URL(route.request().url()).pathname;
        let data = { items: [], categories: [], currencies: [], posts: [], total: 0 };
        if (p.endsWith('/config')) data = { entries: [] };
        if (p.endsWith('/products/1')) data = { id: 1, name: '图片预览测试', cover: '/cover.svg', price_cents: 1000, stock_type: 'card', stock: 10, stock_visible: true, skus: [], controls: [], reviews: [], description: '<p>点击查看详情</p><a href="/leave"><img src="/detail.svg" alt="详情第一张"></a><img src="/second.svg" alt="详情第二张">' };
        return route.fulfill({ json: data });
      });
      await page.goto(`http://127.0.0.1:${server.address().port}/product/1`);
      await page.locator('.pd-cover').click();
      const viewer = page.getByRole('dialog', { name: '图片预览' });
      await expect(viewer).toBeVisible();
      await expect(viewer.getByRole('button', { name: '放大图片', exact: true })).toBeEnabled();
      await viewer.getByRole('button', { name: '放大图片', exact: true }).click();
      await expect(viewer.locator('.iv-zoom')).toHaveText('125%');
      await viewer.getByRole('button', { name: '向右旋转图片' }).click();
      await expect(viewer.locator('img')).toHaveAttribute('style', /rotate\(90deg\)/);
      await viewer.getByRole('button', { name: '向左旋转图片' }).click();
      await expect(viewer.locator('img')).toHaveAttribute('style', /rotate\(0deg\)/);
      await viewer.getByRole('button', { name: '关闭图片预览' }).click();
      await expect(viewer).toHaveCount(0);
      await page.locator('.pd-desc img').first().focus();
      await page.keyboard.press('Enter');
      await expect(viewer).toBeVisible();
      await expect(viewer.locator('.iv-title')).toHaveText('详情第一张');
      await page.keyboard.press('ArrowRight');
      await expect(viewer.locator('.iv-title')).toHaveText('详情第二张');
      await expect(viewer.locator('.iv-zoom')).toHaveText('100%');
      const lastButton = viewer.locator('button:not(:disabled)').last();
      await lastButton.focus();
      await page.keyboard.press('Tab');
      await expect(viewer.getByRole('button', { name: '关闭图片预览' })).toBeFocused();
      await page.keyboard.press('Shift+Tab');
      await expect(lastButton).toBeFocused();
      await page.keyboard.press('Escape');
      await expect(viewer).toHaveCount(0);
      await expect(page.locator('.pd-desc img').first()).toBeFocused();
      assert.equal(await page.evaluate(() => document.body.style.overflow), '');
      assert.equal(new URL(page.url()).pathname, '/product/1');
      await page.locator('.pd-desc img').last().click();
      await expect(viewer.getByRole('button', { name: '放大图片', exact: true })).toBeEnabled();
      assert(await viewer.evaluate(el => el.scrollWidth <= innerWidth), 'toolbar must fit viewport');
      await page.screenshot({ path: path.join(out, `preview-${width}-${fallback}.png`) });
      await viewer.locator('.iv-stage').click({ position: { x: 2, y: 2 } });
      await expect(viewer).toHaveCount(0);
      assert.deepEqual(errors, []);
      await page.close();
      console.log(`PASS image preview width=${width}, legacy=${fallback}`);
    }
    console.log('Screenshots:', out);
  } finally { await browser.close(); server.close(); }
})().catch(e => { console.error(e); server.close(); process.exitCode = 1; });
