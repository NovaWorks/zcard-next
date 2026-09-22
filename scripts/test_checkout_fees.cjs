/** Build storefront first. All payment APIs are disposable local mocks; no real gateway is contacted. */
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http');
const root = path.resolve(__dirname, '../storefront/dist');
const output = process.env.CHECKOUT_SCREENSHOTS || '/tmp/zcard-checkout-fees';
fs.mkdirSync(output, { recursive: true });
let fee = 50, paid = false, submissions = [];
const channels = [
 {code:'balance',name:'余额支付',driver:'wallet'},
 {code:'epay',name:'易支付',driver:'epay',fee:50,fee_type:'percent',fee_bearer:'user',methods:[{code:'alipay',name:'支付宝'},{code:'wxpay',name:'微信支付'}]},
 {code:'be',name:'USDT · TRC20',driver:'bepusdt',recommended:true,recommend_label:'推荐使用',recommend_description:'支持 USDT 支付'},
];
const quote = body => ({ base_cents:10000,fee_cents:body.channel==='epay' ? fee : 0,total_cents:10000+(body.channel==='epay'?fee:0),fee_type:'percent',fee_rate:fee,fee_bearer:body.channel==='epay'?'user':'merchant',quote_key:body.channel+':'+body.method+':'+fee,channel:body.channel,method:body.method });
const server = http.createServer(async (req,res) => {
 const url = new URL(req.url,'http://localhost');
 if (url.pathname.startsWith('/api/')) {
  res.setHeader('Content-Type','application/json');
  let chunks=[];for await(const chunk of req) chunks.push(chunk);
  const body=chunks.length?JSON.parse(Buffer.concat(chunks)):{};
  let data={items:[],entries:[],total:0};
  if(url.pathname.endsWith('/config'))data={entries:[{key:'site.name',value_json:'"费用验证商店"'}]};
  if(url.pathname.endsWith('/payment/channels'))data={channels};
  if(url.pathname.endsWith('/orders/checkout-test'))data={order_no:'checkout-test',status:paid?'paid':'pending_payment',total_cents:10000,paid_total_cents:paid?10100:0,paid_fee_cents:paid?100:0,created_at:Math.floor(Date.now()/1000),expires_at:Math.floor(Date.now()/1000)+900,items:[{product_id:1,product_name:'测试商品',quantity:1,unit_price_cents:10000}]};
  if(url.pathname.endsWith('/payment/quote')) {data=quote(body);if(body.method==='alipay')await new Promise(r=>setTimeout(r,150));}
  if(url.pathname.endsWith('/payments')) {
   submissions.push(body);
   if(body.quote_key!==quote(body).quote_key){res.statusCode=400;data={message:'支付金额已变化，请刷新费用明细后再次确认'};}
   else data={payment_id:1,type:'qrcode',payload:'local-checkout-test',quote:quote(body)};
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
  for(const width of [1200,390,320]) {
   fee=50;paid=false;submissions=[];const errors=[];
   const page=await browser.newPage({viewport:{width,height:1000}});
   page.on('pageerror',e=>errors.push(e.message));
   await page.goto(`http://127.0.0.1:${server.address().port}/payment/checkout-test?pwd=test-password`);
   const submit=page.locator('.pay-submit');await submit.waitFor();
   await page.waitForFunction(()=>document.querySelector('.pay-submit')?.textContent.includes('100.00')&&!document.querySelector('.pay-submit').disabled);
   assert.equal(await page.locator('.pay-recommend').textContent(),'推荐使用');
   const alipay=page.locator('.pay-channel').filter({hasText:'支付宝'}),wx=page.locator('.pay-channel').filter({hasText:'微信支付'});
   await alipay.click();await wx.click();
   await page.waitForFunction(()=>document.querySelector('.pay-submit')?.textContent.includes('100.50')&&!document.querySelector('.pay-submit').disabled);
   await new Promise(r=>setTimeout(r,250));
   assert.equal(await wx.getAttribute('aria-pressed'),'true');
   await page.screenshot({path:path.join(output,`checkout-${width}.png`),fullPage:true});
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'horizontal overflow');
   // Change rate after the displayed quote. The first click must not start payment.
   fee=100;await submit.click();await page.getByText('支付金额已变化，请刷新费用明细后再次确认').waitFor();
   await page.waitForFunction(()=>document.querySelector('.pay-submit')?.textContent.includes('101.00')&&!document.querySelector('.pay-submit').disabled);
   assert.equal(submissions.length,1);
   await submit.click();await page.locator('.pay-qr-layout').waitFor();
   assert((await page.locator('.payment-total').textContent()).includes('101.00'));
   assert.equal(submissions[1].method,'wxpay');assert.equal(submissions[1].quote_key,'epay:wxpay:100');
   paid=true;await page.getByText('支付成功',{exact:true}).waitFor({timeout:12000});
   assert((await page.locator('.pay-amount').textContent()).includes('101.00'));
   await page.getByText('含支付手续费',{exact:false}).waitFor();
   assert.deepEqual(errors,[]);await page.close();console.log(`checkout ${width}px: passed`);
  }
 } finally {await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
