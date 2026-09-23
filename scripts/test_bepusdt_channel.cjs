/** Built admin smoke test with disposable API fixtures: no production credentials. */
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
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
 const browser = await chromium.launch({headless:true,...(process.env.CHROME_PATH ? {executablePath:process.env.CHROME_PATH} : {})});
 try {
  for (const width of [1440,390]) {
   const page=await browser.newPage({viewport:{width,height:1000}}), errors=[], creates=[], updates=[], deletes=[];
   page.setDefaultTimeout(15000);
   const channels=[];
   const drivers=[{code:'bepusdt',name:'BEpusdt',description:'BEpusdt 原生接口 · CNY 计价，支持固定链与多链多币种收银台',fields:[
    {key:'api_url',label:'网关地址',type:'text',required:true,placeholder:'https://pay.example.com',help:'BEpusdt 服务根地址，不含 /api/v1/order/create-transaction'},
    {key:'api_token',label:'API Token',type:'password',required:true,sensitive:true},
    {key:'checkout_mode',label:'收款模式',type:'select',required:true,default:'fixed',options:[{label:'固定币种与网络（兼容原配置）',value:'fixed'},{label:'多链收银台（用户选择币种与网络）',value:'cashier'}]},
    {key:'currencies',label:'收银台币种',type:'select',multiple:true,options:[{label:'USDT',value:'USDT'},{label:'USDC',value:'USDC'},{label:'TON（GRAM）',value:'GRAM'}],help:'不选表示允许网关全部可用币种'},
    {key:'trade_type',label:'收款网络',type:'select',required:true,default:'usdt.trc20',options:[{label:'USDT · TRC20',value:'usdt.trc20'},{label:'USDT · ERC20',value:'usdt.erc20'},{label:'USDT · BEP20',value:'usdt.bep20'},{label:'USDC · Solana',value:'usdc.solana'}]},
    {key:'timeout',label:'支付期限（秒）',type:'number',default:'1200',help:'180–3600 秒；同时受商品订单剩余期限限制。重试不会延长期限'},
   ]},{code:"epusdt",name:"GM Pay",description:"GM Pay 多链多币种收款",fields:[]}];
   page.on('pageerror',e=>errors.push(e.message));
   await page.addInitScript(()=>{localStorage.setItem('token',JSON.stringify('local-test'));localStorage.setItem('refreshToken',JSON.stringify('local-refresh'));});
   await page.route('**/api/v1/**',async route=>{
    const p=new URL(route.request().url()).pathname,method=route.request().method();let data={items:[],total:0};
    if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'local-test'},permissions:['*']};
    if(p.endsWith('/payment/drivers'))data={drivers};
    if(p.endsWith('/payment/channels')){
     if(method==='POST'){const body=route.request().postDataJSON();creates.push(body);data={...body,id:channels.length+1,configured_fields:[],callback_url:'https://shop.example/payments/callback/'+body.code};channels.push(data);}else data={channels};
    }
    if(/\/payment\/channels\/\d+$/.test(p)&&method==='PUT'){
     const body=route.request().postDataJSON();updates.push(body);data=Object.assign(channels.find(c=>c.id===Number(p.split('/').pop())),body);data.configured_fields=['api_url','api_token','trade_type','timeout','checkout_mode','currencies'];
    }
    if(/\/payment\/channels\/\d+$/.test(p)&&method==='DELETE'){
     const id=Number(p.split('/').pop());deletes.push(id);channels.splice(channels.findIndex(c=>c.id===id),1);data={};
    }
    await route.fulfill({json:data});
   });
   await page.goto(`http://127.0.0.1:${server.address().port}/admin/payment-channel`);
   await page.getByRole('button',{name:'添加渠道',exact:true}).click();
   const add=page.locator('.n-modal').filter({hasText:'添加支付渠道'});await add.getByText('GM Pay',{exact:true}).waitFor();await add.getByRole('checkbox').filter({hasText:'BEpusdt'}).click();await add.getByRole('button',{name:/添加所选/}).click();
   const config=page.locator('.n-modal').filter({hasText:'配置「BEpusdt」'});await config.waitFor();
   assert.equal(creates[0].driver,'bepusdt');assert(!creates[0].methods_json || creates[0].methods_json==='[]');
   const field=label=>config.locator('.n-form-item').filter({has:page.locator('.n-form-item-label',{hasText:label})});
   await field('网关地址').locator('input').fill('https://pay.example.com');await field('API Token').locator('input').fill('fixture-token');
   assert.equal(await field('API Token').locator('input').getAttribute('type'),'password');
   await field('收款网络').locator('.n-base-selection').click();await page.getByText('USDT · BEP20',{exact:true}).click();await page.getByText('USDT · ERC20',{exact:true}).waitFor({state:'hidden'});
   assert.equal(await field('支付期限').locator('input').inputValue(),'1200');
   assert((await config.innerText()).includes('180–3600 秒'));
   assert(!(await config.innerText()).includes('按模板生成'));
   const box=await config.boundingBox();assert(box.x>=0 && box.x+box.width<=width+1,'modal overflows viewport');
   if(process.env.BEPUSDT_SCREENSHOT_DIR){fs.mkdirSync(process.env.BEPUSDT_SCREENSHOT_DIR,{recursive:true});await page.screenshot({path:path.join(process.env.BEPUSDT_SCREENSHOT_DIR,`bepusdt-${width}.png`),fullPage:true});}
   await config.getByRole('button',{name:'保存',exact:true}).click();await config.waitFor({state:'hidden'});
   const cfg=JSON.parse(updates.at(-1).config_json);assert.equal(cfg.trade_type,'usdt.bep20');assert.equal(Number(cfg.timeout),1200);assert.equal(cfg.api_token,'fixture-token');assert.deepEqual(errors,[]);
   assert.equal(cfg.checkout_mode,'fixed');assert.deepEqual(cfg.currencies,[]);
   await page.getByRole('button',{name:'配置',exact:true}).click();await config.waitFor();
   assert.equal(await field('收银台币种').count(),0);
   await field('收款网络').locator('.n-base-selection').click();await page.getByText('USDC · Solana',{exact:true}).click();
   await field('收款模式').locator('.n-base-selection').click();await page.getByText('多链收银台（用户选择币种与网络）',{exact:true}).click();
   assert.equal(await field('收款网络').count(),0);
   await field('收银台币种').locator('.n-base-selection').click();
   await page.locator('.n-base-select-option').filter({hasText:/^USDC$/}).click();
   await page.locator('.n-base-select-option').filter({hasText:'TON（GRAM）'}).click();
   await config.getByText('渠道参数',{exact:true}).click();
   await config.getByRole('button',{name:'保存',exact:true}).click();await config.waitFor({state:'hidden'});
   let cashier=JSON.parse(updates.at(-1).config_json);assert.equal(cashier.checkout_mode,'cashier');assert.deepEqual(cashier.currencies,['USDC','GRAM']);assert.equal(cashier.trade_type,'usdc.solana');
   await page.reload();await page.getByRole('button',{name:'配置',exact:true}).click();await config.waitFor();
   assert.equal(await field('收款网络').count(),0);assert((await field('收银台币种').innerText()).includes('USDC'));
   // Removing all selections must send [], otherwise the backend merge retains old limits.
   const close=field('收银台币种').locator('.n-tag__close');
   for(let i=await close.count();i>0;i--) { await close.first().click(); await expect(close).toHaveCount(i-1); }
   await config.getByRole('button',{name:'保存',exact:true}).click();await config.waitFor({state:'hidden'});
   assert.deepEqual(JSON.parse(updates.at(-1).config_json).currencies,[]);
   await page.reload();await page.locator('.n-tag__content').filter({hasText:/^已启用$/}).waitFor();
   const remove=page.getByRole('button',{name:'删除',exact:true});
   assert(await remove.isDisabled(),'enabled channel must be stopped before deletion');
   await page.getByRole('switch').click();await page.locator('.n-tag__content').filter({hasText:/^已停用$/}).waitFor();
   assert(await remove.isEnabled(),'disabled channel must allow deletion');
   await remove.click();await page.getByText('删除后将从渠道列表移除，历史支付记录和到账通知仍会保留，确定删除？',{exact:true}).waitFor();
   await page.getByRole('button',{name:'确认',exact:true}).click();await page.getByText('尚未接入任何支付渠道',{exact:true}).waitFor();
   assert.deepEqual(deletes,[1]);await page.reload();await page.getByText('尚未接入任何支付渠道',{exact:true}).waitFor();assert.deepEqual(errors,[]);
   await page.close();console.log(`PASS BEpusdt fixed/cashier configuration, currency clear/reload, disable/delete confirmation and list removal after reload width=${width}`);
  }
 } finally {await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
