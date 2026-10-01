/** Disposable integration fixtures for the built admin UI. No live API is contacted.
 * pnpm --dir admin build
 * node scripts/test_admin_currency_locale.cjs
 */
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const assert = require('node:assert/strict');
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const root = process.env.ADMIN_DIST || path.resolve(__dirname, '../admin/dist');
const server = http.createServer((req, res) => {
  let file = path.join(root, new URL(req.url, 'http://local').pathname.replace(/^\/admin\/?/, ''));
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
  res.setHeader('Cache-Control', 'no-store');
  res.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png' })[path.extname(file)] || 'application/octet-stream');
  res.end(fs.readFileSync(file));
});

function settingsItems(values) {
  const labels = { base_currency: '基础货币 / 默认结算货币', display_currency: '前端显示货币', default_locale: '默认语言', enabled_locales: '启用语言列表' };
  return Object.entries(values).map(([key, value]) => ({ group: 'i18n', key, label: labels[key], value_json: JSON.stringify(value), options: [] }));
}
function field(page, label) {
  return page.locator('.n-form-item').filter({ has: page.locator('.n-form-item-label').filter({ hasText: new RegExp(`^${label}$`) }) });
}
async function openSelect(item) { await item.locator('.n-base-suffix').click(); }
async function option(page, text) {
  await page.locator('.n-base-select-option:visible').filter({ hasText: new RegExp(`^${text}$`) }).click();
}
async function closeSelect(page) { await page.keyboard.press('Escape'); await expect(page.locator('.n-base-select-option:visible')).toHaveCount(0); }
async function screenshot(page, path) {
  await page.waitForLoadState('networkidle');
  await expect(page.locator('#nprogress')).toHaveCount(0);
  await page.screenshot({path, fullPage:true});
}

(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? {executablePath: process.env.CHROME_PATH} : {}) });
  try {
    for (const width of [1440, 390]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } });
      const errors = [], writes = [];
      page.setDefaultTimeout(10000);
      let failCurrencyReads = false, failedReads = 0;
      const values = { base_currency: 'CNY', display_currency: '', default_locale: 'zh_CN', enabled_locales: ['zh_CN', 'en'] };
      const currencies = [
        { code: 'CNY', symbol: '¥', position: 'prefix', precision: 2, rate_json: '1', enabled: true, sort: 0 },
        { code: 'AUD', symbol: 'A$', position: 'prefix', precision: 2, rate_json: '0.2', enabled: true, sort: 1 },
        { code: 'USD', symbol: '$', position: 'prefix', precision: 2, rate_json: '0.14', enabled: false, sort: 2 },
      ];
      page.on('pageerror', error => errors.push(error.message));
      await page.addInitScript(() => {
        localStorage.setItem('token', JSON.stringify('currency-locale-fixture'));
        localStorage.setItem('refreshToken', JSON.stringify('fixture-refresh'));
      });
      await page.route('**/*', async route => {
        const request = route.request(), url = new URL(request.url()), p = url.pathname;
        if (!p.startsWith('/api/v1/')) {
          if (url.origin !== origin && !url.protocol.startsWith('data')) return route.abort();
          return route.continue();
        }
        let data = { items: [], total: 0, templates: [] };
        if (p.endsWith('/auth/profile')) data = { admin: { id: 1, username: 'local-fixture' }, permissions: ['*'] };
        if (p.endsWith('/currencies')) {
          if (failCurrencyReads) {
            failedReads++;
            return route.fulfill({ status: 503, json: { code: 503, reason: 'fixture.UNAVAILABLE', message: '货币服务暂不可用' } });
          }
          data = { currencies };
        }
        if (p.endsWith('/settings')) {
          if (request.method() === 'PUT') {
            const body = request.postDataJSON();
            writes.push({ path: p, body });
            for (const item of body.items) if (item.group === 'i18n') values[item.key] = JSON.parse(item.value_json);
            data = { updated: body.items.length };
          } else data = { items: url.searchParams.get('group') === 'i18n' ? settingsItems(values) : [] };
        }
        if (p.endsWith('/settings/i18n/base_currency') && request.method() === 'PUT') {
          const body = request.postDataJSON();
          writes.push({ path: p, body });
          values.base_currency = JSON.parse(body.value_json);
          const divisor = Number(currencies.find(c => c.code === values.base_currency).rate_json);
          for (const currency of currencies) currency.rate_json = String(Number((Number(currency.rate_json) / divisor).toFixed(8)));
          data = { group: 'i18n', key: 'base_currency', value_json: body.value_json };
        }
        return route.fulfill({ json: data });
      });
      try {
      await page.goto(`${origin}/admin/settings`);
      await page.getByText('语言货币', { exact: true }).click();
      const base = field(page, '基础货币 / 默认结算货币');
      const defaultLocale = field(page, '默认语言');
      const enabledLocales = field(page, '启用语言列表');
      await expect(base.locator('.n-base-selection')).toBeVisible();
      await openSelect(base);
      await expect(page.locator('.n-base-select-option:visible').filter({ hasText: /^CNY（¥）$/ })).toBeVisible();
      await expect(page.locator('.n-base-select-option:visible').filter({ hasText: /^AUD（A\$）$/ })).toBeVisible();
      await expect(page.locator('.n-base-select-option:visible').filter({ hasText: /^USD/ })).toHaveCount(0);
      await closeSelect(page);
      await openSelect(defaultLocale);
      await expect(page.locator('.n-base-select-option:visible').filter({ hasText: /^简体中文$/ })).toBeVisible();
      await expect(page.locator('.n-base-select-option:visible').filter({ hasText: /^English$/ })).toBeVisible();
      await option(page, 'English');
      await expect(defaultLocale).toContainText('English');
      await closeSelect(page);
      await openSelect(enabledLocales);
      await option(page, 'English');
      await closeSelect(page);
      await expect(defaultLocale).toContainText('简体中文');
      await expect(enabledLocales.locator('.n-tag')).toHaveCount(1);
      await openSelect(enabledLocales);
      await option(page, '简体中文');
      await closeSelect(page);
      await expect(enabledLocales.locator('.n-tag')).toHaveCount(1);
      await expect(page.getByText('至少启用一种语言', {exact:true})).toBeVisible();
      await page.getByRole('button', { name: '保存更改', exact: true }).click();
      await expect.poll(() => writes.length).toBe(1);
      assert.deepEqual(values.enabled_locales, ['zh_CN']);
      assert.equal(values.default_locale, 'zh_CN');
      const batch = writes[0].body.items;
      assert.equal(JSON.parse(batch.find(i => i.key === 'default_locale').value_json), 'zh_CN');
      assert.deepEqual(JSON.parse(batch.find(i => i.key === 'enabled_locales').value_json), ['zh_CN']);
      await screenshot(page, `/tmp/zcard-admin-locale-${width}.png`);

      // Both languages and an English default persist when explicitly saved.
      await openSelect(enabledLocales);
      await option(page, 'English');
      await closeSelect(page);
      await openSelect(defaultLocale);
      await option(page, 'English');
      await closeSelect(page);
      await page.getByRole('button', { name: '保存更改', exact: true }).click();
      await expect.poll(() => writes.length).toBe(2);
      assert.equal(values.default_locale, 'en');
      assert.deepEqual(values.enabled_locales, ['zh_CN', 'en']);
      await page.reload();
      await page.getByText('语言货币', {exact:true}).click();
      await expect(defaultLocale).toContainText('English');
      await expect(enabledLocales.locator('.n-tag')).toHaveCount(2);
      await screenshot(page, `/tmp/zcard-admin-locale-english-${width}.png`);

      // Currency tab posts the sole accounting setting and reflects server state.
      await page.getByText('货币', { exact: true }).click();
      const audRow = page.locator('.n-data-table-tr').filter({ has: page.locator('.n-data-table-td').getByText('AUD', { exact: true }) });
      await audRow.getByRole('button', { name: '设为默认结算', exact: true }).click();
      await expect.poll(() => values.base_currency).toBe('AUD');
      await expect(audRow).toContainText('默认结算');
      assert.equal(writes.at(-1).path, '/api/v1/admin/settings/i18n/base_currency');
      assert.equal(writes.at(-1).body.value_json, '"AUD"');
      await expect(audRow.getByRole('button', { name: '停用', exact: true })).toBeDisabled();
      const cnyRow = page.locator('.n-data-table-tr').filter({ has: page.locator('.n-data-table-td').getByText('CNY', { exact: true }) });
      await expect(cnyRow.getByRole('button', { name: '停用', exact: true })).toBeEnabled();
      await audRow.getByText('AUD', {exact:true}).scrollIntoViewIfNeeded();
      await screenshot(page, `/tmp/zcard-admin-currency-${width}.png`);

      await page.reload();
      await page.getByText('语言货币', { exact: true }).click();
      await expect(field(page, '基础货币 / 默认结算货币')).toContainText('AUD');
      await expect(field(page, '默认语言')).toContainText('English');
      await expect(field(page, '启用语言列表').locator('.n-tag')).toHaveCount(2);

      // A failed read never replaces the controlled currency selector with text input.
      failCurrencyReads = true;
      await page.getByText('站点基础', { exact: true }).click();
      await page.getByText('语言货币', { exact: true }).click();
      await expect(base.getByRole('alert')).toContainText('货币列表加载失败');
      await expect(base.locator('.n-base-selection')).toHaveClass(/n-base-selection--disabled/);
      assert.ok(failedReads > 0);
      await screenshot(page, `/tmp/zcard-admin-currency-retry-${width}.png`);
      failCurrencyReads = false;
      await base.getByRole('button', { name: '重试', exact: true }).click();
      await expect(base.getByRole('alert')).toHaveCount(0);
      await openSelect(base);
      await expect(page.locator('.n-base-select-option:visible').filter({ hasText: /^AUD（A\$）$/ })).toBeVisible();
      await closeSelect(page);
      if (width === 1440) {
        // The chosen accounting currency also labels newly entered amounts.
        await page.goto(`${origin}/admin/marketing`);
        await page.getByRole('button', {name:'新增等级',exact:true}).click();
        await expect(page.getByText('充值阈值(AUD)',{exact:true})).toBeVisible();
        await expect(page.getByText('消费阈值(AUD)',{exact:true})).toBeVisible();
        await page.getByRole('button',{name:'取消',exact:true}).click();
        await page.getByText('优惠券',{exact:true}).click();
        await page.getByRole('button',{name:'批量生成',exact:true}).click();
        await expect(page.getByText('面值(AUD)',{exact:true})).toBeVisible();
        await page.getByRole('button',{name:'取消',exact:true}).click();
        await page.getByText('秒杀/促销',{exact:true}).click();
        await page.getByRole('button',{name:'新建秒杀',exact:true}).click();
        await expect(page.getByText('秒杀价(AUD)',{exact:true})).toBeVisible();
        await page.getByRole('button',{name:'取消',exact:true}).click();
      }
      assert.deepEqual(errors, []);
      console.log(`PASS admin currency/locale UI, enabled-only selection, locale linkage, default AUD write/persistence and read retry width=${width}`);
      } catch (error) { await page.screenshot({path: `/tmp/zcard-admin-test-failure-${width}.png`,fullPage:true}); console.error((await page.locator('main').innerText()).slice(0,4000)); throw error; }
      await page.close();
    }
  } finally { await browser.close(); server.close(); }
})().catch(error => { console.error(error); server.close(); process.exitCode = 1; });
