/** Built storefront with disposable API fixtures; verifies method selection and checkout routing. */
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http');
const root = process.env.STOREFRONT_DIST || path.resolve(__dirname, '../storefront/dist');
const methods = [{code:'USDT-TRC20',name:'USDT · TRC20'},{code:'USDC-BSC',name:'USDC · BSC'}];
const submissions = [];
const quote = body => ({base_cents:1000,fee_cents:0,total_cents:1000,quote_key:body.method,channel:'upay',method:body.method});
const server = http.createServer(async (req,res) => {
 const url = new URL(req.url,'http://localhost');
 if (url.pathname.startsWith('/cashier/')) {
  res.setHeader('Content-Type','text/html');return res.end(`<h1>${url.pathname.slice('/cashier/'.length)}</h1>`);
 }
 if (url.pathname.startsWith('/api/')) {
  res.setHeader('Content-Type','application/json');
  let chunks=[];for await(const chunk of req) chunks.push(chunk);
  const body=chunks.length?JSON.parse(Buffer.concat(chunks)):{};
  let data={items:[],entries:[],total:0};
  if(url.pathname.endsWith('/config'))data={entries:[{key:'site.name',value_json:'"多链验证商店"'}]};
  if(url.pathname.endsWith('/payment/channels'))data={channels:[{code:'upay',name:'UPAY PRO',driver:'upay',methods}]};
  if(url.pathname.endsWith('/orders/upay-test'))data={order_no:'upay-test',status:'pending_payment',total_cents:1000,created_at:Math.floor(Date.now()/1000),expires_at:Math.floor(Date.now()/1000)+900,items:[{product_id:1,product_name:'测试商品',quantity:1,unit_price_cents:1000}]};
  if(url.pathname.endsWith('/payment/quote'))data=quote(body);
  if(url.pathname.endsWith('/payments')) {
   submissions.push(body);
   assert(methods.some(m=>m.code===body.method));
   assert.equal(body.channel,'upay');assert.equal(body.quote_key,body.method);
   data={payment_id:1,type:'redirect',payload:`http://127.0.0.1:${server.address().port}/cashier/${body.method}`,quote:quote(body)};
  }
  return res.end(JSON.stringify(data));
 }
 let file=path.join(root,url.pathname);
 if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');
 res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');
 res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
 try {
  for(const width of [1200,375]) {
   const errors=[];
   const page=await browser.newPage({viewport:{width,height:1000}});
   page.on('pageerror',e=>errors.push(e.message));
   await page.goto(`http://127.0.0.1:${server.address().port}/payment/upay-test?pwd=test-password`);
   for(const method of [methods[0],methods[1],methods[0]]) {
    const option=page.locator('.pay-channel').filter({hasText:method.name});await option.click();
    await page.waitForFunction(()=>{const b=document.querySelector('.pay-submit');return b&&!b.disabled;});
    assert.equal(await option.getAttribute('aria-pressed'),'true');
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'horizontal overflow');
    await page.waitForTimeout(250); // Finish the existing selection transition before visual capture.
    await page.screenshot({animations:"disabled",path:`/tmp/zcard-upay-checkout-${width}.png`,fullPage:true});
    const opened=page.context().waitForEvent('page');
    await page.locator('.pay-submit').click();
    const cashier=await opened;await cashier.waitForLoadState();
    assert(cashier.url().endsWith('/cashier/'+method.code));
    assert.equal(submissions.at(-1).method,method.code);
    await cashier.close();
    await page.getByRole('button',{name:'更换支付方式',exact:true}).click();
   }
   assert.deepEqual(errors,[]);await page.close();console.log(`PASS upay checkout selection and routing ${width}px`);
  }
 } finally {await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
