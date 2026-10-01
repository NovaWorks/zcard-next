// Built UI + isolated API fixtures; never contacts a real SMS provider.
const fs = require('node:fs'), path = require('node:path'), http = require('node:http'), assert = require('node:assert/strict');
const {chromium, expect} = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const storefront = path.resolve(__dirname,'../storefront/dist'), admin = path.resolve(__dirname,'../admin/dist');
const product = {id:1,name:'全球接码服务',slug:'sms',description:'<p>选择国家和服务，按实际报价购买。</p>',product_kind:'sms_channel',delivery_kind:'sms_activation',sms_sales_enabled:true,goods_type:'virtual',price_cents:0,stock:-2,status:1,stock_type:'card',fulfillment_mode:'auto',controls:[],skus:[],reviews:[],sms_connection_id:7,sms_markup_bps:1000,sms_markup_amount_cents:5};
const server=http.createServer((req,res)=>{
 const pathname=new URL(req.url,'http://localhost').pathname;const root=pathname.startsWith('/admin')?admin:storefront;
 let file=path.join(root,pathname.replace(/^\/admin\/?/,''));if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');
 res.setHeader('Content-Type',({'.html':'text/html','.js':'application/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');
 let content=fs.readFileSync(file);if(root===storefront&&path.extname(file)==='.html')content=content.toString().replace('<head>','<head><script type="application/json" id="zcard-theme-runtime">'+JSON.stringify({key:'classic',values:{},capabilities:{cart:false},branding:{name:'Fixture',logo:''}})+'</script>');res.end(content);
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base=`http://127.0.0.1:${server.address().port}`;
 const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
 try{
  for(const width of [1440,390]){
   const page=await browser.newPage({viewport:{width,height:980}});const errors=[],buys=[],quotes=[];let created=false,ordered=false,lostResponse=true,refunded=false,sessionState='waiting_sms',lostAction=true; const actions=[]; let operation={operation_request_id:'00000000-0000-4000-8000-000000000001',operation_action:'finish',operation_status:'rejected'};
   page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept());await page.addInitScript(()=>localStorage.setItem('zcard_token','fixture-member'));
   await page.route('**/api/v1/**',async route=>{
    const req=route.request(),url=new URL(req.url()),p=url.pathname;let data={items:[],total:0,categories:[],posts:[],currencies:[],entries:[]};
    if(p.endsWith('/config')) data={entries:[{key:'site.name',value_json:'"Fixture"'},{key:'trade.cart_enabled',value_json:'false'},{key:'i18n.base_currency',value_json:'"CNY"'}]};
    if(p.endsWith('/me'))data={id:1,username:'member',nickname:'会员'};
    if(p.endsWith('/wallet'))data={available_cents:refunded?1000:ordered?600:1000,locked_cents:0,total_cents:1000};
    if(p.endsWith('/products/1'))data=product;
    if(p.endsWith('/sms/options'))data={countries:[{id:'1',name:'美国'},{id:'2',name:'英国'}],platforms:url.searchParams.get('country_id')==='2'?[{id:'8',name:'Telegram'}]:[{id:'7',name:'WhatsApp'},{id:'8',name:'Telegram'}]};
    if(p.endsWith('/sms/offers')){assert.equal(url.searchParams.get('page_size'),'50');data={offers:[{offer_id:'offer-'+url.searchParams.get('platform_id'),name:'美国 · WhatsApp',price_cents:350,stock:3}],has_more:false};}
    if(p.endsWith('/sms/quotes')){quotes.push(req.postDataJSON());data={quote_id:'quote-1',amount_cents:400,currency:'CNY',expires_at:Math.floor(Date.now()/1000)+300,offer_name:'美国 · WhatsApp'};}
    if(p.endsWith('/orders') && req.method()==='POST'){buys.push({body:req.postDataJSON(),key:req.headers()['idempotency-key']});created=true;if(width===1440 && lostResponse){lostResponse=false;return route.abort('failed');}data={order_no:'123',total_cents:400};}
    if(p.endsWith('/orders/123'))data={order_no:'123',status:ordered?'paid':'pending_payment',total_cents:400};
    if(p.endsWith('/payment/channels'))data={channels:[{code:'wallet',driver:'wallet',name:'会员余额',fee:0}]};
    if(p.endsWith('/payments')){ordered=true;if(width===390 && lostResponse){lostResponse=false;return route.abort('failed');}data={payment_id:'55',type:'redirect',payload:'/payment/123'};}

    if(p.endsWith('/sms/sessions'))data={orders:ordered?[{order_no:'123',offer_name:'美国 · WhatsApp',amount_cents:400,sms:{state:sessionState,phase:'query',phone_number:'12025550123',otp_code:sessionState==='sms_received'?'123456':undefined,otp_message:sessionState==='sms_received'?'Your code is 123456':undefined,can_cancel:!refunded&&sessionState==='waiting_sms',can_finish:sessionState==='sms_received',refund_status:refunded?'succeeded':'none',...operation}}]:[]};
    if(p.endsWith('/123/sms/cancel')){actions.push(req.postDataJSON());assert(actions.at(-1).request_id);if(lostAction){lostAction=false;return route.abort('failed');}operation={operation_request_id:actions.at(-1).request_id,operation_action:'cancel',operation_status:'succeeded'};refunded=true;sessionState='canceled';data={state:'canceled',...operation,refund_status:'succeeded'};}

    await route.fulfill({json:data}).catch(()=>{});
   });
   await page.goto(base+'/product/1');await expect(page.getByRole('heading',{name:'全球接码服务',exact:true})).toBeVisible();await expect(page.locator('.pd-crumb')).toHaveCount(0);await expect(page.getByText('选择国家和服务，按实际报价购买。',{exact:true})).toHaveCount(1);
   await expect(page.locator('#sms-country')).toContainText('美国');await page.locator('#sms-country').selectOption('1');await expect(page.locator('#sms-platform')).toContainText('WhatsApp');await page.locator('#sms-platform').selectOption('7');
   const radio=page.getByRole('radio');await expect(radio).toHaveCount(1);await page.locator('#sms-country').selectOption('2');await expect(page.locator('#sms-platform')).not.toContainText('WhatsApp');await expect(page.locator('#sms-platform')).toContainText('Telegram');await page.locator('#sms-platform').selectOption('8');await expect(radio).toHaveCount(1);await page.locator('#sms-country').selectOption('1');await expect(page.locator('#sms-platform')).toContainText('WhatsApp');await page.locator('#sms-platform').selectOption('7');await expect(radio).toHaveCount(1);await radio.check();await page.getByRole('button',{name:'确认价格',exact:true}).click();await expect(page.getByText('价格已更新，请确认最新金额后支付。')).toBeVisible();
   await page.getByRole('button',{name:/余额支付.*4\.00/}).click();await expect(page.getByRole('button',{name:'确认原购买结果',exact:true})).toBeVisible();await expect(page.locator('#sms-country')).toBeDisabled();
   await page.getByRole('button',{name:'确认原购买结果',exact:true}).click();await expect(page.getByText('12025550123',{exact:true})).toBeVisible();assert.equal(quotes.length,1);assert.equal(buys.length,width===1440?2:1);if(buys.length===2)assert.deepEqual(buys[0],buys[1]);assert.equal(buys[0].body.sms_quote_id,'quote-1');assert(!('sms_amount_cents' in buys[0].body));assert(buys[0].key);
   await page.screenshot({path:`/tmp/zcard-open-sms-product-${width}.png`,fullPage:true});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'horizontal overflow');
   sessionState='sms_received';await page.getByRole('button',{name:'刷新',exact:true}).click();await expect(page.getByText('123456',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'结束接码',exact:true})).toBeVisible();
   sessionState='waiting_sms';await page.getByRole('button',{name:'刷新',exact:true}).click();await page.getByRole('button',{name:'取消接码',exact:true}).click();await expect(page.getByRole('button',{name:'确认取消结果',exact:true})).toBeVisible();await page.getByRole('button',{name:'刷新',exact:true}).click();await expect(page.getByRole('button',{name:'确认取消结果',exact:true})).toBeVisible();await page.getByRole('button',{name:'确认取消结果',exact:true}).click();await expect(page.getByText(/已退还.*4\.00/)).toBeVisible();assert.equal(actions.length,2);assert.deepEqual(actions[0],actions[1],'old operation receipt replaced the new intent');
   assert.equal(errors.length,0,errors.join('\n'));await page.close();
  }
  const page=await browser.newPage({viewport:{width:1440,height:1000}});const errors=[];page.on('pageerror',e=>errors.push(e.message));await page.addInitScript(()=>localStorage.setItem('token',JSON.stringify('fixture-admin')));
  await page.route('**/api/v1/**',async route=>{
   const req=route.request(),p=new URL(req.url()).pathname;let data={items:[],total:0,categories:[],connections:[],skus:[],controls:[],extensions:[]};
   if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'admin'},permissions:['*']};
   if(p.endsWith('/captcha-config'))data={enabled:false};
   if(p.endsWith('/products'))data={products:[product],items:[product],total:1};
   if(p.endsWith('/products/1'))data=product;
   await route.fulfill({json:data}).catch(()=>{});
  });
  await page.goto(base+'/admin/product');await expect(page.getByText('商品页实时选价',{exact:true})).toBeVisible();await page.getByRole('button',{name:'编辑',exact:true}).first().click();const modal=page.locator('.n-modal').filter({hasText:'编辑商品'});
  await modal.getByRole('button',{name:'下一步',exact:true}).click();await expect(modal.getByText(/接码渠道商品按用户选择实时定价/)).toBeVisible();await expect(modal.getByText('售价（元）',{exact:true})).toHaveCount(0);
  await page.getByText('规格与控件',{exact:true}).click();await expect(modal.getByText(/无需创建 SKU 或下单控件/)).toBeVisible();assert.equal(errors.length,0,errors.join('\n'));await page.close();
  console.log('OSS channel UI: desktop/mobile, switching countries after choosing a platform, changed retail price, lost create/payment response, one quote/order/debit, SMS, lost action response with stale prior receipt, retail refund and editor passed');
 }finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
