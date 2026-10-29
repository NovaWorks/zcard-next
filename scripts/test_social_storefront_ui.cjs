// Built storefront against isolated fixtures. No supplier or real account is used.
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root=path.resolve(__dirname,'../storefront/dist-theme');
const shots=process.env.SOCIAL_SCREENSHOT_DIR || '/tmp';fs.mkdirSync(shots,{recursive:true});
const server=http.createServer((req,res)=>{
 const name=new URL(req.url,'http://localhost').pathname;
 let file=path.join(root,name);if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');
 res.setHeader('Content-Type',({'.html':'text/html','.js':'application/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)] || 'application/octet-stream');
 let body=fs.readFileSync(file);
 if(file.endsWith('.html'))body=body.toString().replace('<head>','<head><base href="/"><script type="application/json" id="zcard-theme-runtime">'+JSON.stringify({key:'classic',values:{},capabilities:{cart:false},branding:{name:'Service fixture',logo:''}})+'</script>');
 res.end(body);
});
const product={id:1,name:'服务订单测试',slug:'social',description:'<p>填写服务信息后确认总价。</p>',product_kind:'social_service',delivery_kind:'social_service',goods_type:'virtual',price_cents:0,stock:-1,status:1,stock_type:'card',controls:[],skus:[],reviews:[]};
const field={key:'link',label:'服务链接',type:'url',required:true,max_length:500};
function schema(profile){return {version:1,revision:'schema-1',profile,fields:profile==='default'?[field,{key:'quantity',label:'服务数量',type:'integer',required:true,min:10,max:50000}]:profile==='comments'?[field,{key:'comments',label:'评论内容',type:'lines',required:true,min_items:3,max_items:100},{key:'notes',label:'备注行',type:'lines',min_items:2,max_items:2}]:[field],quantity:profile==='default'?{mode:'explicit',field:'quantity',min:10,max:50000}:profile==='comments'?{mode:'derived_lines',lines_field:'comments',line_policy:'all',min:3,max:100}:{mode:'absent'},billing:{mode:'quoted_total',currency:'CNY',verified:true},purchasable:true,cancel:true,refill:true}}
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base=`http://127.0.0.1:${server.address().port}`;
 const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
 try{
  for(const width of [1440,390]){
   for(const profile of ['default','comments','package']){
    const page=await browser.newPage({viewport:{width,height:980}}),errors=[],quotes=[],buys=[],actions=[];
    let paid=false,lostBuy=profile==='default',lostAction=true,operation;
    page.on('pageerror',e=>errors.push(e.message));await page.addInitScript(()=>localStorage.setItem('zcard_token','fixture-member'));
    await page.route('**/api/v1/**',async route=>{
     const req=route.request(),p=new URL(req.url()).pathname;
     let data={items:[],total:0,categories:[],posts:[],currencies:[],entries:[]};
     if(p.endsWith('/config'))data={entries:[{key:'site.name',value_json:'"Fixture"'},{key:'trade.cart_enabled',value_json:'false'},{key:'i18n.base_currency',value_json:'"CNY"'}]};
     if(p.endsWith('/me'))data={id:1,username:'member'};
     if(p.endsWith('/products/1'))data=product;
     if(p.endsWith('/social/schema'))data=schema(profile);
     if(p.endsWith('/social/quotes')){const body=req.postDataJSON();quotes.push(body);data={quote_id:'quote-B',amount_cents:999,currency:'CNY',expires_at:Math.floor(Date.now()/1000)+300,...(profile==='package'?{}:{quantity:profile==='comments'?3:5000})}}
     if(p.endsWith('/social/orders')){buys.push(req.postDataJSON());if(lostBuy){lostBuy=false;return route.abort('failed')}data={order_no:'social-1',total_cents:999}}
     if(p.endsWith('/orders/social-1'))data={order_no:'social-1',status:paid?'paid':'pending_payment',total_cents:999,items:[{id:1,product_id:1,product_name:product.name,quantity:1,unit_price_cents:999,amount_cents:999,delivery_kind:'social_service',fulfillment_type:'upstream'}],created_at:1700000000};
     if(p.endsWith('/payment/channels'))data={channels:[{code:'wallet',driver:'wallet',name:'会员余额',fee:0}]};
     if(p.endsWith('/payments')){paid=true;data={payment_id:1,type:'success'}}
     if(p.endsWith('/orders/social-1/social') || p.endsWith('/social/cancel')){
      if(p.endsWith('/social/cancel')){const body=req.postDataJSON();actions.push(body);operation={operation_action:'cancel',operation_status:'succeeded',operation_request_id:body.request_id};if(lostAction){lostAction=false;return route.abort('failed')}}
      data={state:'partial',phase:'query',snapshot:{version:'2',...(profile==='package'?{...(width===390?{quantity:null}:{}),remains:0,completed_quantity:0,start_count:0}:{quantity:profile==='comments'?3:5000,completed_quantity:profile==='comments'?1:2500,remains:profile==='comments'?2:2500}),settlement_state:'partially_refunded'},paid_amount_cents:999,refunded_amount_cents:499,refund_status:'partially_refunded',can_cancel:!operation,can_refill:false,active:false,...operation};
     }
     await route.fulfill({json:data}).catch(()=>{});
    });
    await page.goto(base+'/product/1');await page.waitForSelector('#social-1-link');
    await page.locator('#social-1-link').fill('https://example.test/channel');
    if(profile==='default'){await page.locator('#social-1-quantity').fill('5000');assert.equal(await page.locator('#social-1-quantity').getAttribute('max'),'50000')}
    if(profile==='comments'){await page.locator('#social-1-comments').fill('first\n\nfirst');await page.locator('#social-1-notes').fill('note\n\nnote');assert.equal(await page.locator('#social-help-comments').innerText(),'已填写 3 行');assert.equal(await page.locator('#social-help-notes').innerText(),'已填写 2 行');assert((await page.locator('.social-form').innerText()).includes('本次服务数量由填写内容计算：3'))}
    if(profile==='package')assert.equal(await page.locator('.social-form input[type=number]').count(),0);
    await page.getByRole('button',{name:'确认总价',exact:true}).click();await page.waitForSelector('.social-price strong');
    assert.equal(quotes.length,1);
    if(profile==='comments'){assert.equal(quotes[0].parameters.comments,'first\n\nfirst','blank and duplicate lines must be submitted unchanged');assert.equal(quotes[0].parameters.notes,'note\n\nnote','non-derived lines must also be submitted unchanged')}
    if(profile==='package')assert.equal(Object.hasOwn(quotes[0].parameters,'quantity'),false);
    await page.evaluate(()=>{document.documentElement.style.scrollBehavior='auto';window.scrollTo(0,0)});
    await page.screenshot({path:path.join(shots,`zcard-social-${profile}-${width}.png`),fullPage:true});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'form overflow');
    await page.getByRole('button',{name:'余额付款',exact:true}).click();
    if(profile==='default')await page.getByRole('button',{name:'继续原订单',exact:true}).click();
    await page.waitForURL(/\/order\/social-1$/);await page.waitForSelector('.social-order');if(profile==='default'){assert.equal(buys.length,2);assert.deepEqual(buys[0],buys[1],'lost response generated another purchase')}
    assert((await page.locator('.social-metrics').innerText()).includes('4.99'),'local refund was not shown');
    if(profile==='package'){
     assert.equal(await page.locator('.social-order progress').count(),0,'package has fabricated unit progress');
     const metrics=await page.locator('.social-metrics').innerText();
     for(const label of ['服务数量','剩余数量','开始时数量','已完成'])assert.equal(metrics.includes(label),false,`package has unit metric ${label}`);
    }
    await page.getByRole('button',{name:'申请取消',exact:true}).click();
    await page.getByRole('button',{name:'确认原操作',exact:true}).click();
    await page.waitForFunction(()=>!Array.from(document.querySelectorAll('button')).some(b=>b.textContent==='确认原操作'));
    assert.equal(actions.length,2);assert.deepEqual(actions[0],actions[1],'lost action generated another operation');
    await page.evaluate(()=>{document.documentElement.style.scrollBehavior='auto';window.scrollTo(0,0)});
    await page.screenshot({path:path.join(shots,`zcard-social-order-${profile}-${width}.png`),fullPage:true});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'order overflow');
    assert.equal(errors.length,0,errors.join('\n'));await page.close();
   }
  }
  console.log('Social storefront: desktop/mobile Default quantity 5000, duplicate comment lines, Package absence, stable purchase/action retries and refund display passed.');
 }finally{await browser.close();await new Promise(r=>server.close(r))}
})().catch(e=>{console.error(e);process.exitCode=1});
