/** Built admin smoke test with disposable API fixtures: no production credentials. */
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
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
   const page=await browser.newPage({viewport:{width,height:1000}}), errors=[], creates=[], updates=[];
   const channels=[];
   const drivers=[{code:'bepusdt',name:'BEpusdt',description:'BEpusdt 原生接口 · CNY 计价、USDT 收款（每个渠道固定一条链）',fields:[
    {key:'api_url',label:'网关地址',type:'text',required:true,placeholder:'https://pay.example.com',help:'BEpusdt 服务根地址，不含 /api/v1/order/create-transaction'},
    {key:'api_token',label:'API Token',type:'password',required:true,sensitive:true},
    {key:'trade_type',label:'收款网络',type:'select',required:true,default:'usdt.trc20',options:[{label:'USDT · TRC20',value:'usdt.trc20'},{label:'USDT · ERC20',value:'usdt.erc20'},{label:'USDT · BEP20',value:'usdt.bep20'}]},
    {key:'timeout',label:'支付期限（秒）',type:'number',default:'1200',help:'180–3600 秒；同时受商品订单剩余期限限制。重试不会延长期限'},
   ]}];
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
     const body=route.request().postDataJSON();updates.push(body);data=Object.assign(channels.find(c=>c.id===Number(p.split('/').pop())),body);data.configured_fields=['api_url','api_token','trade_type','timeout'];
    }
    await route.fulfill({json:data});
   });
   await page.goto(`http://127.0.0.1:${server.address().port}/admin/payment-channel`);
   await page.getByRole('button',{name:'添加渠道',exact:true}).click();
   const add=page.locator('.n-modal').filter({hasText:'添加支付渠道'});await add.getByRole('checkbox').click();await add.getByRole('button',{name:/添加所选/}).click();
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
   await page.close();console.log(`PASS BEpusdt create, masked token, chain selection, fixed CNY fields, timeout and layout width=${width}`);
  }
 } finally {await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
