// Disposable API fixtures: no payment, external service or real account is contacted.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http');
const root=path.resolve(__dirname,'../storefront/dist'),output='/tmp/zcard-manual-services';fs.mkdirSync(output,{recursive:true});
const address='T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb';let submissions=[];
const order={order_no:'SERVICE-TEST',status:'fulfilling',total_cents:600,paid_total_cents:600,created_at:Math.floor(Date.now()/1000),items:[{id:11,product_id:1,product_name:'能量服务 · 一小时',quantity:1,unit_price_cents:600,amount_cents:600,fulfillment_type:'manual',fulfillment_status:'delivering',form_answers_json:JSON.stringify([{name:'接收地址',value:address}])}]};
const level={name:'代理',display_mode:'contact',display_text:'联系客服',acquire_mode:'manual'};
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://localhost');
 if(url.pathname.startsWith('/api/')) {
  let data={items:[],entries:[],total:0}; const chunks=[];for await(const c of req) chunks.push(c);const body=chunks.length?JSON.parse(Buffer.concat(chunks)):{};
  if(url.pathname.endsWith('/config')) data={entries:[['site.name','本地验证'],['trade.query_password',true],['trade.contact_required','none'],['template.show_reviews',false]].map(([key,v])=>({key,value_json:JSON.stringify(v)}))};
  if(url.pathname.endsWith('/products/1')) data={id:1,name:'能量服务',price_cents:600,stock_type:'card',fulfillment_mode:'manual',manual_stock:20,stock:20,stock_visible:true,status:1,description:'付款后人工处理',controls:[{id:7,name:'接收地址',type:'text',required:true,validation:'tron',max_length:34,placeholder:'填写TRON地址'}],skus:[{id:2,name:'一小时',price_cents:600,fulfillment_mode:'follow',stock:20}]};
  if(url.pathname.endsWith('/orders')&&req.method==='POST') {submissions.push(body);data={order_no:'SERVICE-TEST',total_cents:600};}
  if(url.pathname.endsWith('/orders/SERVICE-TEST')) data=order;
  if(url.pathname.endsWith('/delivery/fetch')) data={order_no:'SERVICE-TEST',status:'completed',items:[{delivery_id:91,item_id:11,kind:'service',product_name:'能量服务',sku_name:'一小时',content:'已完成能量交付，请核对接收地址。'}]};
  if(url.pathname.endsWith('/member-level')) data={levels:[level],current:level,recharged_cents:0,consumed_cents:600};
  if(url.pathname.endsWith('/wallet')) data={balance_cents:1000,frozen_cents:0};
  if(url.pathname.endsWith('/my-orders')) data={orders:[{...order,item_count:1,product_summary:'能量服务 · 一小时',manual_pending_count:1}],total:1};
  if(url.pathname.endsWith('/me')) data={id:1,username:'local-test'};
  res.setHeader('Content-Type','application/json');return res.end(JSON.stringify(data));
 }
 let file=path.join(root,url.pathname);if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base=`http://127.0.0.1:${server.address().port}`;
 const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
 try {for(const width of [1280,390,320]){
  const page=await browser.newPage({viewport:{width,height:1000}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(()=>localStorage.setItem('zcard_token','disposable-local-fixture'));
  await page.goto(base+'/product/1');await page.getByPlaceholder('填写TRON地址').waitFor();
  await page.getByPlaceholder('用于取货验证（忘记将无法取货）').fill('test-password');
  const count=submissions.length;await page.locator('.pd-btn-buy').click();assert.equal(submissions.length,count,'missing required field submitted');
  await page.getByPlaceholder('填写TRON地址').fill(address);await page.screenshot({path:path.join(output,`product-${width}.png`),fullPage:true});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'product horizontal overflow');
  await page.locator('.pd-btn-buy').click();await page.waitForURL('**/payment/**');assert.equal(submissions.at(-1).items[0].control_answers['7'],address);assert.equal(submissions.at(-1).items[0].sku_id,2);
  await page.goto(base+'/order/SERVICE-TEST');await page.getByText(address,{exact:true}).waitFor();await page.getByText('人工服务 · 处理中',{exact:false}).waitFor();
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'order horizontal overflow');await page.screenshot({path:path.join(output,`order-${width}.png`),fullPage:true});
  await page.goto(base+'/fetch?order_no=SERVICE-TEST');await page.locator('.query-pwd-input').fill('test-password');await page.locator('.query-btn').click();await page.getByText('已完成能量交付，请核对接收地址。',{exact:true}).waitFor();
  assert.equal(await page.locator('.delivery-item').count(),1);assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'delivery horizontal overflow');await page.screenshot({path:path.join(output,`delivery-${width}.png`),fullPage:true});
  await page.goto(base+'/member?tab=recharge');await page.locator('.level-benefits').waitFor();const txt=await page.locator('.level-benefits').textContent();assert(txt.includes('联系客服'));assert(!txt.includes('7.3'));assert(!txt.includes('9.9'));
  await page.screenshot({path:path.join(output,`level-${width}.png`),fullPage:true});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'member horizontal overflow');
  assert.deepEqual(errors,[]);await page.close();console.log(`manual services ${width}px: passed`);
 }}finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
