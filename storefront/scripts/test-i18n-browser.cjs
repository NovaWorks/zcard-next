// Built storefront and disposable local APIs only. No payment gateway is contacted.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http');
const root = path.resolve(__dirname, '../dist');
const output = process.env.I18N_SCREENSHOTS || '/tmp/zcard-i18n';
fs.mkdirSync(output, { recursive: true });
let defaults = 'en', enabled = ['zh_CN', 'en'];
const entries = () => Object.entries({
 'site.name': '双语店铺', 'site.logo': '', 'trade.cart_enabled': true,
 'i18n.default_locale': defaults, 'i18n.enabled_locales': enabled,
 'i18n.base_currency': 'AUD', 'i18n.display_currency': 'USD',
 'security.captcha_login': false, 'security.captcha_register': false,
 'security.captcha_order': false, 'security.captcha_reset': false,
 'security.register_enabled': true, 'security.register_method': ['username'],
 'trade.contact_required': 'none', 'trade.query_password_required': true,
 'recharge.min_amount': 1000, 'recharge.max_amount': 500000,
 'recharge.gift_tiers': [{ amount: 10000, gift_balance: 1000 }],
 'theme.brand_bar_enabled': false,
}).map(([key, value]) => ({ key, value_json: JSON.stringify(value) }));
const product = { id: 1, name: '商家自定义商品', description: '<p>商家自定义内容保留。</p>', slug: 'fixture', price_cents: 10000, stock: 20, stock_visible: true, stock_status: 'current', product_kind: 'standard', goods_type: 'virtual', delivery_kind: 'card', stock_type: 'card', fulfillment_mode: 'auto', skus: [], reviews: [], controls: [{ id: 17, name: '商家自定义字段', type: 'text', required: false, options: [] }] };
const server = http.createServer((req, res) => {
 const url = new URL(req.url, 'http://localhost');
 let file = path.join(root, url.pathname);
 if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
 res.setHeader('Content-Type', ({'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)] || 'application/octet-stream');
 let content = fs.readFileSync(file);
 if (path.extname(file) === '.html') content = content.toString().replace('<head>', '<head><script type="application/json" id="zcard-theme-runtime">' + JSON.stringify({ key: 'classic', values: {}, capabilities: {cart: true}, public_config: {entries: entries()}, branding: {name: '双语店铺', logo: ''} }) + '</script>');
 res.end(content);
});
const waitText = (page, selector, text) => page.waitForFunction(({selector, text}) => document.querySelector(selector)?.textContent.includes(text), {selector, text});
(async () => {
 await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
 const base = `http://127.0.0.1:${server.address().port}`;
 const browser = await chromium.launch({headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {})});
 try {
  for (const width of [1440, 390]) {
   defaults = 'en'; enabled = ['zh_CN', 'en'];
   const page = await browser.newPage({viewport: {width, height: 1000}});
   const requests = [], errors = [];
   page.on('pageerror', error => errors.push(error.message));
   await page.route('**/api/**', async route => {
    const request = route.request(), url = new URL(request.url()), p = url.pathname;
    requests.push({path: p, locale: url.searchParams.get('locale'), language: request.headers()['accept-language'], method: request.method()});
    const english = url.searchParams.get('locale') === 'en';
    let data = {items: [], total: 0, posts: [], categories: [], banners: [], entries: [], activities: []}, status = 200;
    if (p.endsWith('/config')) data = {entries: entries()};
    if (p.endsWith('/currencies')) data = {currencies: [{code: 'AUD', symbol: 'A$', position: 'prefix', precision: 2, rate_json: '1'}, {code: 'USD', symbol: '$', position: 'prefix', precision: 2, rate_json: '0.67'}]};
    if (p.endsWith('/products')) data = {items: [product], total: 1, page: 1, page_size: 20};
    if (p.endsWith('/products/1')) data = product;
    if (p.endsWith('/posts')) data = {posts: [{id: 1, title: english ? 'English announcement' : '中文公告', slug: 'notice', type: 'notice', published_at: 1700000000}], total: 1};
    if (p.endsWith('/posts/notice')) data = {post: {id: 1, slug: 'notice', title: english ? 'English announcement' : '中文公告', type: 'notice', published_at: 1700000000}, content: english ? '<p>English article content.</p>' : '<p>中文文章内容。</p>'};
    if (p.endsWith('/banners')) data = {banners: []};
    if (p.endsWith('/user/login')) { status = 401; data = {reason: 'identity.LOGIN_FAILED', message: '账号或密码错误'}; }
    if (p.endsWith('/payment/channels')) data = {channels: [{code: 'stripe', name: 'Card', driver: 'stripe', fee: 0, fee_bearer: 'merchant'}]};
    if (p.endsWith('/orders/i18n-order')) data = {order_no: 'i18n-order', status: 'pending_payment', total_cents: 10000, items: [{product_id: 1, product_name: product.name, quantity: 1}], created_at: 1700000000, expires_at: Math.floor(Date.now() / 1000) + 600};
    if (p.endsWith('/payment/quote')) data = {base_cents: 10000, fee_cents: 0, total_cents: 10000, fee_bearer: 'merchant', fee_type: 'fixed', quote_key: 'fixture', channel: 'stripe', method: '', charged_currency: 'USD', charged_units: 6700, charged_precision: 2};
    if (p.endsWith('/me')) data = {id: 1, username: 'fixture'};
    if (p.endsWith('/wallet')) data = {available_cents: 20000, locked_cents: 0};
    await route.fulfill({status, json: data});
   });
   await page.goto(base + '/');
   await waitText(page, '.login-link', 'Sign in');
   assert.equal(await page.locator('html').getAttribute('lang'), 'en');
   await page.locator('.language-switch select').waitFor();
   assert(requests.filter(request => request.path.endsWith('/products')).every(request => request.language === 'en'), 'initial API locale did not use injected default');
   await page.locator('.notice-close').click();
   // Merchant copy stays untouched while all built-in labels change.
   await page.locator('.product-card').first().click();
   await waitText(page, '.pd-btn-buy', 'Buy now');
   assert(decodeURIComponent(await page.locator('.pd-noimg').getAttribute('src')).includes('No image'));
   await page.locator('#order-17').fill('customer input');
   const numbers = page.locator('input[type="number"]');
   await numbers.first().fill('2');
   await page.locator('.language-switch select').selectOption('zh_CN');
   await waitText(page, '.pd-btn-buy', '立即购买');
   assert(decodeURIComponent(await page.locator('.pd-noimg').getAttribute('src')).includes('暂无主图'));
   assert.equal(await page.locator('#order-17').inputValue(), 'customer input');
   assert.equal(await numbers.first().inputValue(), '2');
   assert.equal(await page.locator('.pd-desc').textContent(), '商家自定义内容保留。');
   await page.locator('.pd-btn-cart').click();
   await waitText(page, '.pd-btn-cart', '移除购物车');
   const guestCart = await page.evaluate(() => JSON.parse(localStorage.getItem('zcard_guest_cart')));
   assert.equal(guestCart[0].quantity, 2);
   await page.locator('.language-switch select').selectOption('en');
   await waitText(page, '.pd-btn-cart', 'Remove from cart');
   assert.deepEqual(await page.evaluate(() => JSON.parse(localStorage.getItem('zcard_guest_cart'))), guestCart);
   await page.locator('.cart-link').click();
   await page.locator('.cart-item').waitFor();
   const lookup = page.locator('.cart-checkout-fields input').first();
   await lookup.fill('saved lookup password');
   const selectedBefore = await page.locator('.cart-item-check').isChecked();
   await page.locator('.language-switch select').selectOption('zh_CN');
   assert.equal(await lookup.inputValue(), 'saved lookup password');
   assert.equal(await page.locator('.cart-item-check').isChecked(), selectedBefore);
   await page.locator('.logo').first().click();
   await page.locator('.product-card').first().waitFor();
   await page.locator('.language-switch select').selectOption('en');
   await page.waitForFunction(() => document.body.textContent.includes('English announcement'));
   assert(requests.some(request => request.path.endsWith('/banners') && request.locale === 'en' && request.language === 'en'));
   await page.goto(base + '/posts/notice');
   await waitText(page, '.post-body', 'English article content.');
   await page.locator('.language-switch select').selectOption('zh_CN');
   await waitText(page, '.post-body', '中文文章内容。');
   await page.locator('.language-switch select').selectOption('en');
   await waitText(page, '.post-body', 'English article content.');
   await page.goto(base + '/login');
   await waitText(page, '.field', 'Account');
   await page.locator('input[type="text"]').fill('customer');
   await page.locator('input[type="password"]').fill('secret');
   await page.locator('main button.btn').click();
   await waitText(page, '.error', 'Invalid username or password.');
   await page.locator('.language-switch select').selectOption('zh_CN');
   assert.equal(await page.locator('input[type="text"]').inputValue(), 'customer');
   await waitText(page, '.error', '账号或密码错误');
   await page.locator('.language-switch select').selectOption('en');
   await page.goto(base + '/payment/i18n-order?pwd=fixture');
   await waitText(page, '.pay-submit', 'USD 67.00');
   assert.equal(await page.locator('.pay-submit').isEnabled(), true);
   await waitText(page, '.payment-breakdown', 'AUD');
   await waitText(page, '.payment-gateway-amount', 'USD 67.00');
   assert((await page.locator('.payment-total').textContent()).includes('A$100.00'));
   assert(requests.filter(request => request.path.endsWith('/payment/quote')).every(request => request.language === 'en'));
   assert(!requests.some(request => request.method === 'POST' && request.path.endsWith('/payments')), 'a payment was submitted');
   await page.screenshot({path: path.join(output, `checkout-en-${width}.png`), fullPage: true});
   assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `English layout overflows at ${width}px`);
   // Recharge inputs and limits always use settlement currency, even with USD display.
   await page.evaluate(() => localStorage.setItem('zcard_token', 'fixture'));
   await page.goto(base + '/member?tab=recharge');
   await page.locator('.rc-custom input').fill('100');
   await waitText(page, '.rc-submit', 'USD 67.00');
   assert.equal(await page.locator('.rc-submit').isEnabled(), true);
   assert.equal(await page.locator('.rc-yen').textContent(), 'AUD');
   assert((await page.locator('.rc-limit').textContent()).includes('A$10.00'));
   assert((await page.locator('.rc-tier-amount').first().textContent()).includes('A$100.00'));
   await page.screenshot({path: path.join(output, `recharge-en-${width}.png`), fullPage: true});
   await page.locator('.language-switch select').selectOption('zh_CN');
   assert.equal(await page.locator('.rc-custom input').inputValue(), '100');
   await waitText(page, '.rc-submit', '立即支付 USD 67.00');
   await page.locator('.language-switch select').selectOption('en');
   await page.evaluate(() => localStorage.removeItem('zcard_token'));
   // A stored English preference is ignored immediately when English is disabled.
   enabled = ['zh_CN']; defaults = 'zh_CN';
   await page.goto(base + '/login');
   await waitText(page, '.login-link', '登录');
   assert.equal(await page.locator('.language-switch select').count(), 0);
   assert.equal(await page.evaluate(() => localStorage.getItem('zcard_locale')), null);
   assert.equal(await page.locator('html').getAttribute('lang'), 'zh-CN');
   assert.deepEqual(errors, []);
   await page.screenshot({path: path.join(output, `single-language-${width}.png`), fullPage: true});
   await page.close();
   console.log(`i18n ${width}px: default, saved preference, forms/cart, content refresh, errors, AUD/USD quote, disabled language passed`);
  }
 } finally { await browser.close(); await new Promise(resolve => server.close(resolve)); }
})().catch(error => { console.error(error); process.exitCode = 1; server.close(); });
