// Isolated built storefront fixtures. No supplier account, database, or money is used.
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root=path.resolve(__dirname,'../storefront/dist');
const shots=process.env.CARD_SCREENSHOT_DIR || '/tmp/zcard-card-ui';fs.mkdirSync(shots,{recursive:true});
const server=http.createServer((req,res)=>{
 const name=new URL(req.url,'http://localhost').pathname;
 let file=path.join(root,name);if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');
 res.setHeader('Content-Type',({'.html':'text/html','.js':'application/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)] || 'application/octet-stream');
 let body=fs.readFileSync(file);
 if(file.endsWith('.html'))body=body.toString().replace('<head>','<head><base href="/"><script type="application/json" id="zcard-theme-runtime">'+JSON.stringify({key:'classic',values:{},capabilities:{cart:true},branding:{name:'Card fixture',logo:''}})+'</script>');
 res.end(body);
});
const controls=[{id:11,name:'备注',type:'textarea',required:true,max_length:1000},{id:12,name:'账号密码',type:'password',required:true,max_length:1000},{id:13,name:'版本',type:'select',required:true,options:['v1','v2'],option_labels:{v1:'第一版本',v2:'第二版本'}}];
const product={id:1,name:'数量报价测试',slug:'card',description:'<p>按本次数量和填写内容核算总价。</p>',product_kind:'card',delivery_kind:'card',goods_type:'virtual',product_property:'virtual',price_cents:100,stock:-1,status:1,stock_type:'card',stock_status:'on_demand',fulfillment_mode:'upstream',controls,skus:[{id:10,name:'标准版',price_cents:100,stock:-1,fulfillment_mode:'upstream'},{id:20,name:'备用版',price_cents:100,stock:-1,fulfillment_mode:'upstream'}],reviews:[],card_purchase:{minimum:5,maximum:0,dynamic_quote:true,preselection:true}};
const settings={'site.name':'Fixture','trade.cart_enabled':true,'trade.query_password':false,'trade.contact_required':'none','i18n.base_currency':'CNY','template.show_reviews':false,'captcha.order':false};
async function prepare(browser,width,profile){
 const page=await browser.newPage({viewport:{width,height:Number(process.env.CARD_UI_HEIGHT||980)}}),errors=[],quotes=[],buys=[],drafts=[];
 let current={...product,controls:profile.endsWith('cart')?[]:controls,card_purchase:{...product.card_purchase,minimum:profile==='quantity'?5:1},...(['quantity','points'].includes(profile)?{points_required:50}:{}),...(profile==='legacy-cart'?{card_purchase:undefined}:{})},createAttempt=0;
 page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{if(profile==='points')return d.accept();errors.push('Unexpected dialog: '+d.message());await d.dismiss()});
 if(profile==='points')await page.addInitScript(()=>localStorage.setItem('zcard_token','points-fixture-member'));
 if(profile.endsWith('cart'))await page.addInitScript(()=>localStorage.setItem('zcard_guest_cart',JSON.stringify([{id:1,product_id:1,sku_id:10,quantity:150,product_name:'旧版购物车',price_cents:100,stock:1000,valid:true,max_quantity:0,points_only:false,points_required:0,added_at:1}])));
 await page.route('**/api/v1/**',async route=>{
  const req=route.request(),p=new URL(req.url()).pathname;
  let data={items:[],total:0,categories:[],posts:[],currencies:[],entries:[]};
  if(p.endsWith('/config'))data={entries:Object.entries(settings).map(([key,value])=>({key,value_json:JSON.stringify(value)}))};
  if(p.endsWith('/products/1'))data=current;
  if(p.endsWith('/me'))data={id:1,username:'points-fixture-member',points:1000};
  if(p.endsWith('/card/drafts')){const input=req.postDataJSON();drafts.push(input);if(input.sku_id===10)await new Promise(r=>setTimeout(r,100));data={items:[{selection:`opaque-sku-${input.sku_id}`,preview:`卡密预览 ${input.sku_id}`,premium_cents:37}],total:1}}
  if(p.endsWith('/orders/quote')){quotes.push(req.postDataJSON());data={quote_key:`quote-${quotes.length}`,total_cents:profile==='points'?undefined:(quotes.length===1?12345:12445),shipping_cents:0,expires_at:Math.floor(Date.now()/1000)+300,...(profile==='points'&&quotes.length>1?{points_total:quotes.length===2?50:60}:{})}}
  if(p.endsWith('/orders')&&req.method()==='POST'){
   const input=req.postDataJSON();buys.push({input,key:req.headers()['idempotency-key']});createAttempt++;
   if(['quantity','points'].includes(profile)&&createAttempt===1)return route.fulfill({status:409,json:{reason:'order.QUOTE_CHANGED',message:'报价变化，请重新确认'}});
   if(profile==='quantity'&&createAttempt===2)return route.abort('failed');
   data={order_no:'card-fixture-1',total_cents:profile==='points'?undefined:12445,expires_at:Math.floor(Date.now()/1000)+300};
  }
  if(p.endsWith('/orders/card-fixture-1'))data={order_no:'card-fixture-1',status:'pending_payment',total_cents:12445,expires_at:Math.floor(Date.now()/1000)+300,items:[{id:1,product_id:1,product_name:current.name,quantity:1,unit_price_cents:12445,amount_cents:12445,delivery_kind:'card'}]};
  await route.fulfill({json:data}).catch(()=>{});
 });
 return {page,errors,quotes,buys,drafts};
}
async function fillForm(page,prefix='order'){
 await page.locator(`#${prefix}-11`).fill(' first\n\nfirst ');
 await page.locator(`#${prefix}-12`).fill(' keep spaces ');
 await page.locator(`#${prefix}-13`).selectOption('v2');
}
async function checkLayout(page,name,width){
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,`${name} overflow`);
 await page.evaluate((focusSelection)=>{document.documentElement.style.scrollBehavior='auto';const block=focusSelection?document.querySelector('.card-selection'):null;window.scrollTo(0,block?Math.max(0,block.getBoundingClientRect().top+scrollY-110):0)},name==='selected-card-form');
 await page.screenshot({path:path.join(shots,`${name}-${width}.png`),fullPage:false});
}
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base=`http://127.0.0.1:${server.address().port}`;
 const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
 try{for(const width of (process.env.CARD_UI_WIDTH?[Number(process.env.CARD_UI_WIDTH)]:[1440,390])){
  if(!process.env.CARD_UI_PROFILE||process.env.CARD_UI_PROFILE==='quantity'){
   const {page,errors,quotes,buys}=await prepare(browser,width,'quantity');await page.goto(base+'/product/1');await page.waitForSelector('.pd-qty-input');
   assert.equal(await page.locator('.pd-qty-input').inputValue(),'5');assert.equal(await page.locator('.pd-qty-input').getAttribute('min'),'5');assert.equal(await page.locator('.pd-qty-input').getAttribute('max'),null);
   assert.equal(await page.getByRole('button',{name:'查看可选卡密',exact:true}).count(),0,'minimum>1 exposed single-card selection');
   assert.equal(await page.locator('.pd-btn-points').isDisabled(),true,'minimum bypassed through points');
   await page.locator('.pd-qty-input').fill('150');await fillForm(page);assert.equal(await page.locator('#order-12').getAttribute('type'),'password');
   assert.equal(await page.locator('#order-13 option:checked').innerText(),'第二版本');
   await page.getByRole('button',{name:'立即购买',exact:true}).click();await page.locator('dialog[open]').waitFor();
   assert.equal(await page.locator('dialog #shipping-name').count(),0,'digital quote requested postal address');
   await page.getByRole('button',{name:'核算实际总价',exact:true}).click();await page.locator('.shipping-quote').waitFor();
   assert.equal(quotes.length,1);assert.equal(quotes[0].items[0].quantity,150);assert.deepEqual(quotes[0].items[0].control_answers,{'11':' first\n\nfirst ','12':' keep spaces ','13':'v2'});
   assert((await page.locator('.shipping-quote strong').innerText()).includes('123.45'),'actual total replaced with unit times quantity');
   await page.getByRole('button',{name:'确认金额，创建订单',exact:true}).click();await page.getByRole('button',{name:'核算实际总价',exact:true}).waitFor();
   assert.equal(await page.locator('.shipping-quote').count(),0,'changed quote remained confirmable');
   await page.getByRole('button',{name:'核算实际总价',exact:true}).click();await page.getByRole('button',{name:'确认金额，创建订单',exact:true}).click();
   await page.getByRole('button',{name:'确认原下单结果',exact:true}).waitFor();
   assert.equal(await page.getByRole('button',{name:'关闭',exact:true}).isDisabled(),true);assert.equal(await page.getByRole('button',{name:'重新核算金额',exact:true}).isDisabled(),true);
   await checkLayout(page,'quantity-uncertain',width);await page.getByRole('button',{name:'确认原下单结果',exact:true}).click();await page.waitForURL(/\/payment\/card-fixture-1$/);
   assert.equal(buys.length,3);assert.deepEqual(buys[1],buys[2],'uncertain retry changed input or request identity');assert.equal(buys[1].input.quote_key,'quote-2');assert.equal(errors.length,0,errors.join('\n'));await page.close();
  }
  if(!process.env.CARD_UI_PROFILE||process.env.CARD_UI_PROFILE==='selection'){
   const {page,errors,quotes,drafts}=await prepare(browser,width,'selection');await page.goto(base+'/product/1');await page.locator('.pd-qty-input').waitFor();
   await page.locator('.pd-btn-cart').waitFor();await page.getByRole('button',{name:'查看可选卡密',exact:true}).click();await page.getByRole('button',{name:/卡密预览 10/}).click();
   assert.equal(await page.locator('.pd-qty-input').inputValue(),'1');assert.equal(await page.locator('.pd-qty-input').isDisabled(),true);assert.equal(await page.locator('.pd-btn-cart').count(),0,'selected card entered ordinary cart');
   await page.getByRole('button',{name:'改为随机交付',exact:true}).click();assert.equal(await page.locator('.pd-qty-input').isDisabled(),false);await page.locator('.pd-btn-cart').waitFor();
   await page.getByRole('button',{name:'查看可选卡密',exact:true}).click();await page.locator('.pd-sku',{hasText:'备用版'}).click();await page.getByRole('button',{name:'查看可选卡密',exact:true}).click();await page.getByRole('button',{name:/卡密预览 20/}).click();
   assert.equal(await page.getByRole('button',{name:/卡密预览 10/}).count(),0,'late old SKU selection leaked');
   await fillForm(page);await checkLayout(page,'selected-card-form',width);await page.getByRole('button',{name:'立即购买',exact:true}).click();await page.getByRole('button',{name:'核算实际总价',exact:true}).click();await page.locator('.shipping-quote').waitFor();
   assert.equal(quotes[0].items[0].card_selection,'opaque-sku-20');assert.equal(quotes[0].items[0].sku_id,20);assert.equal(quotes[0].items[0].quantity,1);assert(drafts.length>=2);
   await checkLayout(page,'selected-card',width);assert.equal(errors.length,0,errors.join('\n'));await page.close();
  }
  if(!process.env.CARD_UI_PROFILE||process.env.CARD_UI_PROFILE==='points'){
   const {page,errors,quotes,buys}=await prepare(browser,width,'points');await page.goto(base+'/product/1');await page.locator('.pd-qty-input').waitFor();await fillForm(page);
   await page.getByPlaceholder('用于取货验证（忘记将无法取货）',{exact:true}).fill('test1234');
   await page.locator('.pd-btn-points').click();await page.getByRole('button',{name:'核算实际总价',exact:true}).click();await page.getByRole('alert').filter({hasText:'积分报价暂不可用'}).waitFor();
   assert.equal(await page.locator('.shipping-quote').count(),0,'missing points quote became confirmable');assert.equal(buys.length,0);
   await page.getByRole('button',{name:'核算实际总价',exact:true}).click();await page.locator('.shipping-points').waitFor();assert((await page.locator('.shipping-points').innerText()).includes('50'));
   await page.getByRole('button',{name:'确认金额，创建订单',exact:true}).click();await page.getByRole('button',{name:'核算实际总价',exact:true}).waitFor();assert.equal(await page.locator('.shipping-quote').count(),0,'changed points quote remained confirmable');
   await page.getByRole('button',{name:'核算实际总价',exact:true}).click();await page.locator('.shipping-points').waitFor();assert((await page.locator('.shipping-points').innerText()).includes('60'));
   assert.equal(quotes.length,3);assert.equal(quotes[2].use_points,true);await checkLayout(page,'points-quote',width);
   await page.getByRole('button',{name:'确认金额，创建订单',exact:true}).click();await page.waitForURL(/\/fetch$/);assert.equal(buys.length,2);assert.equal(buys[1].input.quote_key,'quote-3');assert.equal(buys[1].input.use_points,true);assert.equal(errors.length,0,errors.join('\n'));await page.close();
  }
  if(!process.env.CARD_UI_PROFILE||process.env.CARD_UI_PROFILE==='legacy-cart'){
   const {page,errors,quotes,buys}=await prepare(browser,width,'legacy-cart');await page.goto(base+'/cart');await page.locator('.cart-qty-input').waitFor();
   assert.equal(await page.locator('.cart-qty-input').getAttribute('max'),'99','legacy max=0 accidentally means unlimited');
   await page.locator('.cart-qty-input').fill('99');await page.locator('.cart-qty-input').blur();
   await page.waitForFunction(()=>JSON.parse(localStorage.getItem('zcard_guest_cart'))[0].quantity===99);
   const stored=await page.evaluate(()=>JSON.parse(localStorage.getItem('zcard_guest_cart')));assert.equal(stored[0].quantity,99);
   assert.equal(quotes.length,0);assert.equal(buys.length,0);assert.equal(errors.length,0,errors.join('\n'));await page.close();
  }
  if(!process.env.CARD_UI_PROFILE||process.env.CARD_UI_PROFILE==='cart'){
   const {page,errors,quotes}=await prepare(browser,width,'cart');await page.goto(base+'/cart');await page.locator('.cart-qty-input').waitFor();await page.getByPlaceholder('查询密码（取货用，≥4 位）',{exact:true}).waitFor();
   assert.equal(await page.locator('.cart-qty-input').inputValue(),'150','old cart was truncated');assert.equal(await page.locator('.cart-qty-input').getAttribute('max'),null);
   await page.locator('.cart-qty-input').fill('200');await page.locator('.cart-qty-input').blur();
   await page.locator('.cart-checkout-btn').click();await page.getByRole('button',{name:'核算实际总价',exact:true}).click();await page.locator('.shipping-quote').waitFor();
   assert.equal(quotes[0].items[0].quantity,200);assert.equal(Object.hasOwn(quotes[0].items[0],'card_selection'),false,'ordinary cart carried selection token');
   await checkLayout(page,'cart-quote',width);assert.equal(errors.length,0,errors.join('\n'));await page.close();
  }
 }
 console.log(`Card storefront PASS: widths=${process.env.CARD_UI_WIDTH||'1440,390'} profiles=${process.env.CARD_UI_PROFILE||'quantity,selection,points,legacy-cart,cart'}; provider limits, exact total/points, frozen requests, optional opaque selection and legacy cart.`);
 }finally{await browser.close();await new Promise(r=>server.close(r))}
})().catch(e=>{console.error(e);process.exitCode=1});
