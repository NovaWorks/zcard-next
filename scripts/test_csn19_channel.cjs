// Build admin first. Local API fixtures only; no provider key or live purchase.
const assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http');
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const root = process.env.CSN19_ADMIN_DIST || path.resolve(__dirname, '../admin/dist');
const server = http.createServer((req, res) => {
 let file = path.join(root, new URL(req.url, 'http://localhost').pathname.replace(/^\/admin\/?/, ''));
 if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
 res.setHeader('Content-Type', ({'.html':'text/html','.js':'application/javascript','.css':'text/css'})[path.extname(file)] || 'application/octet-stream');
 res.end(fs.readFileSync(file));
});
(async () => {
 await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
 const browser = await chromium.launch({headless:true,...(process.env.CHROME_PATH ? {executablePath:process.env.CHROME_PATH} : {})});
 try {
  for (const width of [1440, 390]) {
   const page = await browser.newPage({viewport:{width,height:1000}}); page.setDefaultTimeout(12000);
   const errors = [], writes = []; let connection={id:1,name:'历史服务渠道',driver:'csn19',base_url:'https://19csn.com/api/v2',status:'active',credentials_set:true,exchange_rate:1};
   page.on('pageerror', e => errors.push(e.message));
   await page.addInitScript(() => {localStorage.setItem('token',JSON.stringify('local-test'));localStorage.setItem('refreshToken',JSON.stringify('local-refresh'));});
   await page.route('**/api/v1/**', async route => {
    const req = route.request(), p = new URL(req.url()).pathname;
    let data = {items:[],entries:[],total:0,connections:[],tasks:[],categories:[],products:[],pending:{}};
    if (p.endsWith('/auth/profile')) data = {admin:{id:1,username:'local-test'},permissions:['*']};
    if (p.endsWith('/supply/connections')) {
     if (req.method() === 'POST') {const body=req.postDataJSON();writes.push(body);connection={...body,id:1,status:'active'};data=connection;}
     else data = {connections:connection?[connection]:[],total:connection?1:0};
    }
    if (p.endsWith('/supply/connections/1') && req.method()==='PUT') {writes.push(req.postDataJSON());connection={...connection,...req.postDataJSON()};data=connection;}
    if (p.endsWith('/supply/connections/1/ping')) data={ok:true,site_name:'19csn',balance:10084,currency:'USD'};
    await route.fulfill({json:data});
   });
   await page.goto(`http://127.0.0.1:${server.address().port}/admin/channel`);
   await page.getByRole('button',{name:'新增渠道',exact:true}).click();
   const dialog=page.locator('.n-modal').filter({hasText:'新增渠道'});
   await dialog.locator('.n-base-selection').first().click();
   await expect(page.getByText('19csn（服务渠道）',{exact:true})).toHaveCount(0);
   await expect(page.getByText('ZCard（自有协议）',{exact:true}).last()).toBeVisible();
   await page.keyboard.press('Escape');
   const box=await dialog.boundingBox();assert(box.x>=0 && box.x+box.width<=width,'channel dialog overflows');
   await page.keyboard.press('Escape');
   await dialog.waitFor({state:'hidden'});
   // Existing native connections remain readable, but have no configuration form.
   await page.getByRole('button',{name:width===390?'操作 ▾':'更多 ▾',exact:true}).click();
   await expect(page.getByText('编辑',{exact:true})).toHaveCount(0);
   assert.equal(writes.length,0);
   assert.deepEqual(errors,[]);await page.close();console.log(`PASS public CSN configuration hidden, ZCard available and legacy configuration inaccessible width=${width}`);
  }
 } finally {await browser.close();await new Promise(resolve=>server.close(resolve));}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
