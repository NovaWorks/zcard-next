// Build admin first. Local API fixtures only; no provider key or live purchase.
const assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http');
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const root = path.resolve(__dirname, '../admin/dist');
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
   const errors = [], writes = []; let connection;
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
   await dialog.getByPlaceholder('如：主站独角').fill('19csn 测试渠道');
   await dialog.locator('.n-base-selection').first().click();
   await page.getByText('19csn（服务渠道）',{exact:true}).click();
   await expect(dialog.locator('.n-alert')).toContainText('仅需 API Key');
   const credential=dialog.locator('.n-form-item').filter({has:page.locator('.n-form-item-label').getByText('API Key',{exact:true})});
   await credential.locator('input').fill('fixture-secret');
   await expect(dialog.getByText('Api Secret',{exact:true})).toHaveCount(0);
   await dialog.getByPlaceholder('https://up.example.com').fill('https://19csn.com/api/v2');
   const box=await dialog.boundingBox();assert(box.x>=0 && box.x+box.width<=width,'channel dialog overflows');
   await dialog.getByRole('button',{name:'创建',exact:true}).click();
   await dialog.waitFor({state:'hidden'});
   await expect(page.getByText('19csn 测试渠道',{exact:true})).toBeVisible();
   assert.equal(writes.length,1);assert.equal(writes[0].driver,'csn19');assert.equal(writes[0].base_url,'https://19csn.com/api/v2');
   assert.deepEqual(JSON.parse(writes[0].credentials),{api_key:'fixture-secret'});
   if (width === 390) {await page.getByRole('button',{name:'操作 ▾',exact:true}).click();await page.getByText('测试连接',{exact:true}).click();}
   else await page.getByRole('button',{name:'测试',exact:true}).click();
   await expect(page.getByText('连接成功：19csn，余额 100.84 USD',{exact:true})).toBeVisible();
   assert.deepEqual(errors,[]);await page.close();console.log(`PASS 19csn channel selection, single key, save and responsive layout width=${width}`);
  }
 } finally {await browser.close();await new Promise(resolve=>server.close(resolve));}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
