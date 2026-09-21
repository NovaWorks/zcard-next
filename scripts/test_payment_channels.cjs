/** Run after pnpm --dir admin build; uses disposable mocked channels. */
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http'), assert = require('node:assert/strict');
const root = path.resolve(__dirname, '../admin/dist');
const server = http.createServer((req, res) => {
 let file = path.join(root, new URL(req.url, 'http://localhost').pathname.replace(/^\/admin\/?/, ''));
 if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
 res.setHeader('Content-Type', ({'.html':'text/html','.js':'application/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)] || 'application/octet-stream');
 res.end(fs.readFileSync(file));
});
(async () => {
 await new Promise(r => server.listen(0, '127.0.0.1', r));
 const browser = await chromium.launch({ headless:true, ...(process.env.CHROME_PATH ? {executablePath:process.env.CHROME_PATH} : {}) });
 try {
  for (const width of [1440,390]) {
   const page = await browser.newPage({viewport:{width,height:1000}}), errors=[], creates=[];
   const channels = [{id:1,code:'epay',name:'上游 A',driver:'epay',config_json:'{"pid":"1000","key":"****"}',configured_fields:['pid','key'],enabled:true}, {id:2,code:'balance',name:'余额支付',driver:'wallet',config_json:'{}',enabled:true}];
   const original = JSON.stringify(channels[0]);
   const drivers = [{code:'wallet',name:'余额支付',fields:[]}, {code:'epay',name:'易支付',description:'易支付兼容网关',fields:[{key:'pid',label:'商户号',type:'text',required:true},{key:'key',label:'商户密钥',type:'password',required:true,sensitive:true}]}];
   page.on('pageerror',e=>errors.push(e.message));
   await page.addInitScript(()=>{localStorage.setItem('token',JSON.stringify('local-test'));localStorage.setItem('refreshToken',JSON.stringify('local-refresh'));});
   await page.route('**/api/v1/**', async route => {
    const p = new URL(route.request().url()).pathname, method = route.request().method();
    let data = {items:[],total:0};
    if(p.endsWith('/auth/profile')) data={admin:{id:1,username:'local-test'},permissions:['*']};
    if(p.endsWith('/payment/drivers')) data={drivers};
    if(p.endsWith('/payment/channels')) {
     if(method==='POST') {
      const body=route.request().postDataJSON(); creates.push(body);
      assert(!channels.some(c=>c.code===body.code),'duplicate channel code');
      data={...body,id:channels.length+1,configured_fields:[],callback_url:'/payments/callback/'+body.code}; channels.push(data);
     } else data={channels};
    }
    if(/\/payment\/channels\/\d+$/.test(p)&&method==='PUT') {
     const ch=channels.find(c=>c.id===Number(p.split('/').pop())); Object.assign(ch,route.request().postDataJSON());
     ch.configured_fields=['pid','key']; data=ch;
    }
    await route.fulfill({json:data});
   });
   await page.goto(`http://127.0.0.1:${server.address().port}/admin/payment-channel`);
   for (const sequence of [2,3]) {
    await page.getByRole('button',{name:'添加渠道',exact:true}).click();
    const add=page.locator('.n-modal').filter({hasText:'添加支付渠道'});
    await expect(add.getByRole('checkbox')).toHaveCount(1);
    await expect(add).toContainText('可继续添加独立配置');
    await add.getByRole('checkbox').click();
    await add.getByRole('button',{name:/添加所选/}).click();
    const config=page.locator('.n-modal').filter({hasText:`配置「易支付 ${sequence}」`});
    await expect(config).toBeVisible();
    const created=creates.at(-1);
    assert.equal(created.code,`epay-${sequence}`); assert.equal(created.driver,'epay'); assert.equal(created.config_json,'{}'); assert.equal(created.enabled,false);
    assert.equal(JSON.parse(created.methods_json).length,2);
    await config.locator('.n-form-item').filter({has:page.locator('.n-form-item-label',{hasText:'渠道名称'})}).locator('input').fill(`上游 ${sequence}`);
    await config.locator('.n-form-item').filter({has:page.locator('.n-form-item-label',{hasText:'商户号'})}).locator('input').fill(`200${sequence}`);
    await config.locator('.n-form-item').filter({has:page.locator('.n-form-item-label',{hasText:'商户密钥'})}).locator('input').fill(`fixture-key-${sequence}`);
    await config.getByRole('button',{name:'保存',exact:true}).click();
    await expect(config).toHaveCount(0);
    assert.equal(channels.at(-1).enabled,true);
    assert.equal(JSON.parse(channels.at(-1).config_json).pid,`200${sequence}`);
    assert.equal(JSON.stringify(channels[0]),original,'existing credentials changed');
   }
   await page.reload();
   await expect(page.locator('main')).toContainText('上游 2'); await expect(page.locator('main')).toContainText('上游 3');
   assert.deepEqual(errors,[]); await page.close();
   console.log(`PASS repeated epay creation, independent config, wallet singleton, reload width=${width}`);
  }
 } finally { await browser.close(); server.close(); }
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
