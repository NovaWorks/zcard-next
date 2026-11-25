// Completed service orders, original targets and delivery routing in the built UI.
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root=process.env.SOCIAL_STOREFRONT_DIST || path.resolve(__dirname,'../storefront/dist-theme');
const shots=process.env.SOCIAL_SCREENSHOT_DIR || '/tmp';fs.mkdirSync(shots,{recursive:true});
const target='https://www.tiktok.com/@buyer/video/123456789?lang=zh&tracking='+('a'.repeat(180));
const no='service-completed-1';
const fields=[{id:'link',name:'目标链接',type:'url',value:target},{id:'quantity',name:'服务数量',type:'integer',value:'5000'},{id:'comments',name:'评论内容',type:'lines',value:'same\nsame\n中文'}];
const item={id:1,product_id:1,product_name:'TK 视频播放',quantity:1,unit_price_cents:999,amount_cents:999,delivery_kind:'social_service',fulfillment_type:'upstream',fulfillment_status:'delivered',form_answers_json:JSON.stringify(fields)};
const server=http.createServer((req,res)=>{
 let file=path.join(root,new URL(req.url,'http://localhost').pathname);
 if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');
 res.setHeader('Content-Type',({'.html':'text/html','.js':'application/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)] || 'application/octet-stream');
 let body=fs.readFileSync(file);
 if(file.endsWith('.html'))body=body.toString().replace('<head>','<head><base href="/"><script type="application/json" id="zcard-theme-runtime">'+JSON.stringify({key:'classic',values:{},capabilities:{cart:false},branding:{name:'Order fixture',logo:''}})+'</script>');
 res.end(body);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const base=`http://127.0.0.1:${server.address().port}`;
 const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
 try {
  for(const width of [1440,390,375]) {
   const page=await browser.newPage({viewport:{width,height:980},reducedMotion:'reduce'}),errors=[];let cardFetches=0;
   page.on('pageerror',err=>errors.push(err.message));await page.addInitScript(()=>localStorage.setItem('zcard_token','fixture-member'));
   await page.route('**/api/v1/**',async route=>{
    const p=new URL(route.request().url()).pathname;
    let data={items:[],entries:[],orders:[],total:0,categories:[],posts:[],currencies:[]};
    if(p.endsWith('/config'))data={entries:[{key:'site.name',value_json:'"Order fixture"'},{key:'i18n.base_currency',value_json:'"CNY"'}]};
    if(p.endsWith('/me'))data={id:1,username:'buyer'};
    if(p.endsWith('/my-orders'))data={orders:[{order_no:no,status:'delivered',total_cents:999,item_count:1,product_summary:'TK 视频播放',target_link:target,created_at:1700000000}],total:1};
    if(p.endsWith('/orders/'+no))data={order_no:no,status:'delivered',total_cents:999,items:[item],created_at:1700000000};
    if(p.endsWith('/orders/'+no+'/social'))data={state:'completed',phase:'done',snapshot:{quantity:5000,completed_quantity:5000,remains:0},paid_amount_cents:999,refunded_amount_cents:0,refund_status:'none',can_cancel:false,can_refill:false,active:false};
    if(p.includes('/delivery'))cardFetches++;
    await route.fulfill({json:data}).catch(()=>{});
   });
   await page.goto(base+'/member?tab=orders');
   const historyLink=page.locator('.order-target a:visible');await historyLink.waitFor();assert.equal(await historyLink.getAttribute('href'),target);
   assert.equal(await page.locator('.table-desktop .badge:visible, .mcard .badge:visible').first().innerText(),'已完成');
   assert.equal(await page.getByRole('link',{name:'查看交付',exact:true}).getAttribute('href'),'/order/'+no);
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'history target overflow');
   await page.screenshot({path:path.join(shots,`social-order-history-${width}.png`),fullPage:true});
   await page.getByRole('link',{name:'详情',exact:true}).click();
   await page.locator('.social-state').waitFor();assert.equal(await page.locator('.social-state').innerText(),'已完成');
   assert.equal(await page.locator('.od-answer-link').getAttribute('href'),target);
   assert.equal(await page.locator('.od-answer-link').getAttribute('rel'),'noopener noreferrer');
   assert((await page.locator('.od-answers').innerText()).includes('5000'),'quantity missing from completed order');
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'completed order target overflow');
   await page.screenshot({path:path.join(shots,`social-order-completed-${width}.png`),fullPage:true});
   await page.goto(base+'/fetch?order_no='+no);await page.waitForURL('**/order/'+no);
   assert.equal(cardFetches,0,'service order entered card delivery');
   assert.deepEqual(errors,[]);await page.close();
  }
  console.log('PASS completed target in desktop/mobile order history and detail, original quantity and direct service delivery routing');
 } finally {await browser.close();await new Promise(resolve=>server.close(resolve));}
})().catch(err=>{console.error(err);server.close();process.exitCode=1});
