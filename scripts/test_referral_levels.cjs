// Isolated built UI fixtures; never contacts real users, payments or a production server.
const assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const output = '/tmp/zcard-referral-ui'; fs.mkdirSync(output, { recursive: true });
const server = http.createServer((req, res) => {
 const url = new URL(req.url, 'http://localhost');
 const admin = url.pathname.startsWith('/admin');
 const root = path.resolve(__dirname, admin ? '../admin/dist' : '../storefront/dist');
 let file = path.join(root, admin ? url.pathname.replace(/^\/admin\/?/, '') : url.pathname);
 if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
 res.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'application/javascript', '.css': 'text/css', '.svg': 'image/svg+xml' })[path.extname(file)] || 'application/octet-stream');
 res.end(fs.readFileSync(file));
});
(async () => {
 await new Promise(r => server.listen(0, '127.0.0.1', r));
 const base = `http://127.0.0.1:${server.address().port}`;
 const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) });
 try {
  for (const width of [1440, 390]) {
   const page = await browser.newPage({ viewport: { width, height: 1100 } }), errors = [];
   let changed, configured, registration, adjustCalls = 0, failAdjust = true, finishAdjust, finishRefresh;
   let pauseRefresh = false;
   const levels = [
    { id: 1, name: '推荐会员', enabled: true, acquire_mode: 'manual', display_mode: 'public', discount: 9000 },
    { id: 2, name: '高级会员', enabled: true, acquire_mode: 'auto', display_mode: 'public', discount: 8500 }
   ];
   const customer = { id: 7, username: 'local-customer', status: 'active', level_id: 1, level_name: '推荐会员', level_source: 'referral', referral_level_id: 1, referral_level_name: '推荐会员', invite_level_id: 0, promo_code: 'ABCDEFGH' };
   page.on('pageerror', e => errors.push(e.message));
   await page.addInitScript(() => { localStorage.setItem('token', JSON.stringify('local-test')); localStorage.setItem('refreshToken', JSON.stringify('local-refresh')); });
   await page.route('**/api/v1/**', async route => {
    const req = route.request(), url = new URL(req.url()), p = url.pathname;
    let data = { items: [], entries: [], total: 0, categories: [], orders: [], users: [], products: [], roles: [], levels: [], pending: {} };
    if (p.endsWith('/auth/profile')) data = { admin: { id: 1, username: 'local-test' }, permissions: ['*'] };
    if (p.endsWith('/admin/users')) {
     if (pauseRefresh) await new Promise(resolve => { finishRefresh = resolve; });
     data = { users: [customer], total: 1 };
    }
    if (p.endsWith('/admin/wallet/7')) data = { available_cents: customer.balance_cents || 1000, locked_cents: 0, total_cents: customer.balance_cents || 1000 };
    if (p.endsWith('/admin/wallet/7/adjust')) {
     adjustCalls++;
     assert.deepEqual(req.postDataJSON(), { amount_cents: 1000, reason: '本地调账验证' });
     if (failAdjust) { await route.fulfill({ status: 400, json: { code: 400, reason: 'wallet.ADJUST_FAILED', message: '本地模拟调账失败' } }); return; }
     await new Promise(resolve => { finishAdjust = resolve; });
     customer.balance_cents = 2000;
     data = { available_cents: 2000, locked_cents: 0, total_cents: 2000 };
    }
    if (p.endsWith('/admin/users/7')) data = { user: customer, inviter_username: 'local-agent', invitees_count: 0 };
    if (p.endsWith('/admin/member-levels')) data = { levels };
    if (p.endsWith('/users/7/member-level')) {
     changed = req.postDataJSON();
     Object.assign(customer, { manual_level_id: changed.level_id, level_id: changed.level_id, level_name: '高级会员', level_source: 'manual' }); data = {};
    }
    if (p.endsWith('/users/7/invite-level')) { configured = req.postDataJSON(); Object.assign(customer, { invite_level_id: configured.level_id, invite_level_name: '推荐会员' }); data = {}; }
    if (p.endsWith('/config')) data = { entries: [['site.name', '本地验证'], ['security.register_enabled', true], ['security.captcha_register', false], ['security.register_method', ['username']]].map(([key, value]) => ({ key, value_json: JSON.stringify(value) })) };
    if (p.endsWith('/captcha/config')) data = { login: false, register: false, reset: false, order: false };
    if (p.endsWith('/register-config')) data = { enabled: true, methods: ['username'] };
    if (p.endsWith('/captcha-config')) data = { enabled: false, register: false, login: false };
    if (p.endsWith('/member-level/invite-benefit')) data = { valid: url.searchParams.get('code') === 'ABCDEFGH', has_benefit: url.searchParams.get('code') === 'ABCDEFGH', level: url.searchParams.get('code') === 'ABCDEFGH' ? levels[0] : undefined };
    if (p.endsWith('/user/register')) { registration = req.postDataJSON(); data = { user_id: 7, token: 'local-fixture', promo_code: 'NEWCODEA' }; }
    if (p.endsWith('/user/me')) data = { user_id: 7, username: 'local-customer' };
    if (p.endsWith('/member-level')) data = { levels, current: levels[0], source: 'referral', has_referral_level: true, recharged_cents: 0, consumed_cents: 0 };
    if (p.endsWith('/wallet')) data = { balance_cents: 0, frozen_cents: 0 };
    await route.fulfill({ json: data });
   });
   await page.goto(base + '/admin/user');
   await page.getByRole('button', { name: '余额', exact: true }).click();
   const walletDialog = page.locator('.n-modal').filter({ hasText: '调整余额' });
   await walletDialog.locator('.n-input-number input').fill('10');
   await walletDialog.getByPlaceholder('必填；将写入审计日志').fill('本地调账验证');
   await walletDialog.getByRole('button', { name: '确认调整', exact: true }).click();
   await page.getByText('本地模拟调账失败', { exact: true }).waitFor();
   assert(await walletDialog.isVisible(), 'failed adjustment must keep the dialog open');
   assert.equal(await walletDialog.getByPlaceholder('必填；将写入审计日志').inputValue(), '本地调账验证');
   failAdjust = false; pauseRefresh = true;
   const adjustmentSent = page.waitForRequest(req => req.url().endsWith('/wallet/7/adjust'));
   await walletDialog.getByRole('button', { name: '确认调整', exact: true }).click();
   await adjustmentSent;
   const pendingSubmit = walletDialog.locator('button').filter({ hasText: '确认调整' });
   await pendingSubmit.evaluate(button => button.click());
   assert.equal(adjustCalls, 2, 'pending adjustment must not submit twice');
   assert(await pendingSubmit.isDisabled());
   finishAdjust();
   await walletDialog.waitFor({ state: 'hidden' });
   // Closing must not depend on a slow follow-up balance/list read.
   assert.equal(adjustCalls, 2);
   for (let attempt = 0; !finishRefresh && attempt < 100; attempt++) await new Promise(resolve => setTimeout(resolve, 10));
   assert(finishRefresh, 'list refresh was not requested');
   pauseRefresh = false; finishRefresh();
   await page.locator('.n-data-table tbody').getByText('20.00', { exact: true }).waitFor();
   console.log(`PASS wallet failure retention, duplicate guard, immediate close and refreshed balance width=${width}`);
   await page.getByRole('button', { name: '详情', exact: true }).click();
   await page.getByRole('button', { name: '修改等级', exact: true }).click();
   let dialog = page.locator('.n-dialog');
   await dialog.locator('.n-base-selection').click();
   await page.getByText('高级会员 · 8.5 折', { exact: true }).click();
   assert(await dialog.getByRole('button', { name: '保存', exact: true }).isDisabled());
   await dialog.getByPlaceholder('请输入本次调整原因').fill('本地验证后台改级');
   await page.locator('.n-base-select-menu').waitFor({ state: 'hidden' });
   const box = await dialog.boundingBox(); assert(box.x >= 0 && box.x + box.width <= width, 'admin dialog overflow');
   await page.screenshot({ path: path.join(output, `admin-level-${width}.png`), fullPage: true });
   await dialog.getByRole('button', { name: '保存', exact: true }).click();
   await page.getByText('后台指定', { exact: true }).waitFor();
   assert.equal(changed.level_id, 2); assert.equal(changed.clear_referral, false);
   await page.getByRole('button', { name: '设置赠送等级', exact: true }).click();
   dialog = page.locator('.n-dialog');
   await dialog.locator('.n-base-selection').click();
   await page.getByText('推荐会员 · 9 折', { exact: true }).click();
   await dialog.getByPlaceholder('请输入本次调整原因').fill('本地验证推荐赠送');
   await dialog.getByRole('button', { name: '保存', exact: true }).click();
   await page.waitForFunction(() => !document.querySelector('.n-dialog'));
   assert.equal(configured.level_id, 1); assert.equal(customer.manual_level_id, 2);
   await page.goto(base + '/register?ref=ABCDEFGH');
   await page.getByText(/注册成功即可获得\s*推荐会员\s*待遇，会员价享\s*9\s*折。/).waitFor();
   await page.getByPlaceholder('用户名', { exact: true }).fill('new-customer');
   await page.getByPlaceholder('至少 6 位').fill('test-only-password');
   await page.screenshot({ path: path.join(output, `register-${width}.png`), fullPage: true });
   assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'register overflow');
   await page.getByRole('button', { name: '注册', exact: true }).click();
   await page.waitForURL('**/member');
   assert.equal(registration.invite_code, 'ABCDEFGH'); assert(!('level_id' in registration));
   await page.getByText('已通过推荐注册获得会员等级，无需先满足充值或消费门槛。').waitFor();
   await page.screenshot({ path: path.join(output, `member-${width}.png`), fullPage: true });
   assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'member overflow');
   assert.deepEqual(errors, []);
   await page.close(); console.log(`referral UI ${width}px: passed`);
  }
 } finally { await browser.close(); await new Promise(r => server.close(r)); }
})().catch(e => { console.error(e); server.close(); process.exitCode = 1; });
