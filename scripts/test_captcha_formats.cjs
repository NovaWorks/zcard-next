/** Captcha input regression against both production builds and a local mock API.
 * Build admin/ and storefront/ first; PLAYWRIGHT_MODULE and CHROME_PATH are optional.
 */
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const roots = { admin: path.resolve(__dirname, '../admin/dist'), storefront: path.resolve(__dirname, '../storefront/dist') };
const image = 'data:image/svg+xml,' + encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="200" height="40"><rect width="200" height="40" fill="#fff"/><text x="10" y="28">Ab1Cd2Ef</text></svg>');
let format = {}, imageID = 0, submitted;
const entries = Object.entries({ 'site.name': '验证码测试', 'i18n.default_locale': 'zh_CN', 'i18n.enabled_locales': ['zh_CN', 'en'], 'security.captcha_login': true, 'theme.brand_bar_enabled': false }).map(([key, value]) => ({ key, value_json: JSON.stringify(value) }));
const server = http.createServer((req, res) => {
  const pathname = new URL(req.url, 'http://localhost').pathname;
  res.setHeader('Cache-Control', 'no-store');
  if (pathname.startsWith('/api/')) {
    res.setHeader('Content-Type', 'application/json');
    if (pathname.endsWith('/captcha/image')) return res.end(JSON.stringify({ captcha_id: String(++imageID), image_base64: image, ...format }));
    if (pathname.endsWith('/captcha-config')) return res.end(JSON.stringify({ enabled: true }));
    if (pathname.endsWith('/config')) return res.end(JSON.stringify({ entries }));
    if (pathname.endsWith('/login')) {
      let body = ''; req.on('data', chunk => body += chunk); req.on('end', () => {
        submitted = JSON.parse(body); res.statusCode = 401;
        res.end(JSON.stringify({ reason: 'identity.LOGIN_FAILED', message: '账号或密码错误' }));
      }); return;
    }
    return res.end(JSON.stringify({ items: [], posts: [], banners: [], currencies: [], total: 0 }));
  }
  const admin = pathname.startsWith('/admin/');
  const root = roots[admin ? 'admin' : 'storefront'];
  let file = path.join(root, admin ? pathname.slice('/admin/'.length) : pathname);
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
  res.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png' })[path.extname(file)] || 'application/octet-stream');
  let content = fs.readFileSync(file);
  if (!admin && path.extname(file) === '.html') content = content.toString().replace('<head>', '<head><script type="application/json" id="zcard-theme-runtime">' + JSON.stringify({ key: 'classic', values: {}, capabilities: {}, branding: { name: '验证码测试', logo: '' }, public_config: { entries } }) + '</script>');
  res.end(content);
});
async function enter(input, value) {
  // Simulate a pasted/IME value so filtering is checked before the final length limit.
  await input.evaluate((element, text) => { element.value = text; element.dispatchEvent(new Event('input', { bubbles: true })); }, value);
}
(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = 'http://127.0.0.1:' + server.address().port;
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) });
  try {
    for (const app of ['storefront', 'admin']) for (const width of [1280, 390]) {
      format = {}; submitted = undefined;
      const page = await browser.newPage({ viewport: { width, height: 900 } });
      const errors = []; page.on('pageerror', error => errors.push(error.message));
      await page.goto(base + (app === 'admin' ? '/admin/login' : '/login'));
      const input = page.getByLabel(app === 'admin' ? '图形验证码' : '验证码', { exact: true });
      const picture = app === 'admin' ? page.locator('img[title="点击刷新"]') : page.locator('.captcha-img');
      await picture.waitFor();
      assert.equal(await input.getAttribute('maxlength'), '4');
      assert.equal(await input.getAttribute('inputmode'), 'numeric');
      await enter(input, 'a1b2c3d4');
      await page.waitForFunction(() => document.querySelector('input[aria-label="验证码"], input[aria-label="图形验证码"]')?.value === '1234');
      assert.equal(await input.inputValue(), '1234', 'legacy response should retain four numeric digits');
      format = { length: 6, charset: 'letter' }; await picture.click();
      await page.waitForFunction(() => document.querySelector('input[aria-label="验证码"], input[aria-label="图形验证码"]')?.maxLength === 6);
      assert.equal(await input.inputValue(), '', 'refresh must clear old input');
      assert.equal(await input.getAttribute('inputmode'), 'text');
      assert((await input.getAttribute('placeholder')).includes('不区分大小写'));
      await enter(input, 'Ab1C-dE!fG');
      await page.waitForFunction(() => document.querySelector('input[aria-label="验证码"], input[aria-label="图形验证码"]')?.value === 'AbCdEf');
      const username = app === 'admin' ? page.getByPlaceholder('请输入用户名') : page.getByPlaceholder('用户名 / 邮箱 / 手机号');
      const password = app === 'admin' ? page.getByPlaceholder('请输入密码') : page.getByPlaceholder('密码', { exact: true });
      await username.fill('captcha-test'); await password.fill('captcha-test-password');
      const submit = app === 'admin' ? page.getByRole('button', { name: '确认', exact: true }) : page.locator('main button.btn');
      const response = page.waitForResponse(response => response.url().endsWith('/login') && response.request().method() === 'POST');
      await submit.click(); await response;
      assert.equal(submitted.captcha_code, 'AbCdEf'); assert(submitted.captcha_id);
      await page.waitForFunction(() => document.querySelector('input[aria-label="验证码"], input[aria-label="图形验证码"]')?.value === '');
      format = { length: 8, charset: 'mixed' }; await picture.click();
      await page.waitForFunction(() => document.querySelector('input[aria-label="验证码"], input[aria-label="图形验证码"]')?.maxLength === 8);
      await enter(input, 'a1B2-c3D4!5');
      await page.waitForFunction(() => document.querySelector('input[aria-label="验证码"], input[aria-label="图形验证码"]')?.value === 'a1B2c3D4');
      const mixedResponse = page.waitForResponse(response => response.url().endsWith('/login') && response.request().method() === 'POST');
      await submit.click(); await mixedResponse; assert.equal(submitted.captcha_code, 'a1B2c3D4');
      await page.waitForFunction(() => document.querySelector('input[aria-label="验证码"], input[aria-label="图形验证码"]')?.value === '');
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'captcha must fit narrow layout');
      const box = await picture.boundingBox(); assert(box.x >= 0 && box.x + box.width <= width, 'captcha picture should remain visible');
      assert.deepEqual(errors, []);
      await page.close(); console.log(`${app} captcha formats ${width}px: passed`);
    }
  } finally { await browser.close(); await new Promise(resolve => server.close(resolve)); }
})().catch(error => { console.error(error); server.close(); process.exitCode = 1; });
