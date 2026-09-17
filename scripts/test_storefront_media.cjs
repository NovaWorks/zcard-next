/** Browser regression using production SSG output and a disposable local mock API.
 * After installing storefront dependencies:
 * PLAYWRIGHT_MODULE=/path/to/@playwright/test node scripts/test_storefront_media.cjs
 * Rebuilds storefront/dist with fixture data; normal release builds replace it.
 * Screenshots and build logs go to ZCARD_TEST_OUTPUT or a temporary directory.
 */
const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const {createServer}=require('node:http');
const {readFileSync,existsSync,writeFileSync,mkdirSync,mkdtempSync}=require('node:fs');
const {join,extname,resolve}=require('node:path');
const {spawn}=require('node:child_process');
const assert=require('node:assert/strict');
const project=resolve(__dirname,'../storefront');
const out=process.env.ZCARD_TEST_OUTPUT || mkdtempSync(join(require('node:os').tmpdir(),'zcard-sales-'));
mkdirSync(out,{recursive:true});
const root=join(project,'dist');
const products=[1,2].map(id=>({id,name:'测试商品'+id,slug:'test-'+id,description:'<p>测试商品详情</p><video src="/uploads/tutorial.mp4" controls playsinline preload="none"></video>',cover:'',price_cents:1000,stock_type:'card',stock:100,stock_visible:true,category_id:1,sales_count:12345,controls:[],reviews:[],skus:[]}));
let state={enabled:true,sort:'sales',gate:Promise.resolve(),requests:[]};
const server=createServer(async(req,res)=>{
 const s=state;const u=new URL(req.url,'http://localhost');const p=u.pathname;
 res.setHeader('Cache-Control','no-store');
 if(p.startsWith('/api/')){
  res.setHeader('Content-Type','application/json');
  if(p.endsWith('/config')){
   await s.gate;
   if(s.fail){res.statusCode=503;return res.end(JSON.stringify({message:'unavailable'}));}
   const values={'site.name':'销量验证商店','site.logo':'','trade.cart_enabled':false,'template.sort_by':s.sort};
   values['template.bg_image']='/bg.svg'; values['template.bg_image_mobile']='/bg-mobile.svg';
   if(s.enabled!==undefined){values['template.show_sales']=s.enabled;values['template.show_reviews']=s.enabled;}
   return res.end(JSON.stringify({entries:Object.entries(values).map(([key,value])=>({key,value_json:JSON.stringify(value)}))}));
  }
  if(p==='/api/v1/storefront/products'){
   s.requests.push(u.searchParams.get('sort'));
   return res.end(JSON.stringify({items:products,total:2,page:1,page_size:20}));
  }
  if(/^\/api\/v1\/storefront\/products\/\d+$/.test(p))return res.end(JSON.stringify(products[Number(p.split('/').pop())-1]));
  if(p==='/api/v1/storefront/posts')return res.end(JSON.stringify({posts:[{id:1,slug:'tutorial',type:'blog',title:'视频教程',published_at:1700000000}],total:1}));
  if(p==='/api/v1/storefront/posts/tutorial')return res.end(JSON.stringify({post:{id:1,slug:'tutorial',type:'blog',title:'视频教程'},content:'<video controls preload="none"><source src="/uploads/tutorial.mp4" type="video/mp4"></video>'}));
  if(p.endsWith('/categories'))return res.end(JSON.stringify({categories:[{id:1,name:'测试分类'}]}));
  return res.end(JSON.stringify({items:[],posts:[],banners:[],currencies:[],total:0}));
 }
 if(p==='/bg.svg'||p==='/bg-mobile.svg'){res.setHeader('Content-Type','image/svg+xml');return res.end('<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><rect width="100" height="100" fill="#134e4a"/></svg>');}
 if(p==='/uploads/tutorial.mp4'){res.setHeader('Content-Type','video/mp4');return res.end(readFileSync(resolve(__dirname,'../server/internal/mods/media/testdata/tutorial.mp4')));}
 let file=join(root,p==='/'?'index.html':p);
 if(!existsSync(file)&&!extname(p))file=join(root,p+'.html');
 if(!existsSync(file)){res.statusCode=404;return res.end();}
 const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.png':'image/png','.svg':'image/svg+xml'};
 res.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');
 let content=readFileSync(file);
 if(extname(file)==='.html')content=content.toString().replace('<head>','<head><script type="application/json" id="zcard-theme-runtime">'+JSON.stringify({key:'classic',values:{},capabilities:{cart:false},branding:{name:'销量验证商店',logo:''}})+'</script>');
 res.end(content);
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const url='http://127.0.0.1:'+server.address().port;
 let log='';const build=spawn('pnpm',['build'],{cwd:project,env:{...process.env,VITE_SSG_API:url}});build.stdout.on('data',d=>log+=d);build.stderr.on('data',d=>log+=d);
 const code=await new Promise(r=>build.on('exit',r));writeFileSync(join(out,'build.log'),log);assert.equal(code,0,log);
 for(const file of ['index.html','product/1.html','product/2.html']){
  const html=readFileSync(join(root,file),'utf8');
  assert(!/id="pd-reviews"|class="pd-review-entry"|class="pc-sales"|<span>销量<\/span>|<option[^>]*value="sales"/.test(html),file+' contains sales before client settings');
 }
 console.log('SSG enabled-at-build regression: passed');
 const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH ? {executablePath:process.env.CHROME_PATH} : {})});const results=[];
 try{
  for(const test of [
   {name:'off-desktop',enabled:false,width:1280},
   {name:'off-mobile',enabled:false,width:390},
   {name:'on',enabled:true,width:1280},
   {name:'legacy-default',enabled:undefined,width:390},
   {name:'failure',enabled:false,fail:true,width:1280},
  ]){
   console.log('Starting',test.name);
   let release;state={...test,sort:'sales',gate:new Promise(r=>release=r),requests:[]};
   const ctx=await browser.newContext({viewport:{width:test.width,height:900}});const page=await ctx.newPage();const errors=[];
   const pending=new Set();page.on('request',r=>pending.add(r));page.on('response',r=>pending.delete(r.request()));page.on('requestfinished',r=>pending.delete(r));page.on('requestfailed',r=>pending.delete(r));
   page.on('pageerror',e=>errors.push(e.message));page.on('console',m=>{if(/hydration/i.test(m.text()))errors.push(m.text())});
   await page.addInitScript(()=>{window.salesSeen=[];window.reviewsSeen=[];window.backgroundLost=[];window.checkBackground=false;new MutationObserver(()=>{if(document.querySelector('#pd-reviews')||document.querySelector('.pd-review-entry'))window.reviewsSeen.push(location.pathname);if(window.checkBackground && document.querySelector('.app') && getComputedStyle(document.querySelector('.app')).backgroundImage==='none')window.backgroundLost.push(location.pathname);if(document.querySelector('.pc-sales')||[...document.querySelectorAll('.pd-stat span')].some(el=>el.textContent==='销量'))window.salesSeen.push(location.pathname)}).observe(document,{childList:true,subtree:true,characterData:true});});
   await page.goto(url+'/product/1',{waitUntil:'domcontentloaded'});
   await expect(page.locator('.pd-name')).toHaveText('测试商品1');
   await expect(page.locator('.pd-stat').filter({hasText:'销量'})).toHaveCount(0);
   await expect(page.locator('#pd-reviews')).toHaveCount(0);assert.equal((await page.evaluate(()=>window.reviewsSeen)).length,0);
   const before=await page.evaluate(()=>window.salesSeen);assert.equal(before.length,0,'sales flashed while config pending');
   release();await expect(page.locator('.topbar .logo')).toHaveAttribute('aria-busy','false');await expect.poll(()=>pending.size,{timeout:5000}).toBe(0).catch(e=>{console.log('pending requests', [...pending].map(r=>({url:r.url(),type:r.resourceType()})));throw e});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   const on=test.enabled!==false&&!test.fail;
   await expect(page.locator('#pd-reviews')).toHaveCount(on?1:0);
   if(!on)assert.equal((await page.evaluate(()=>window.reviewsSeen)).length,0);
   const video=page.locator('.pd-desc video');await expect(video).toHaveCount(1);await video.evaluate(async v=>{v.muted=true;await v.play()});await expect.poll(()=>video.evaluate(v=>v.readyState)).toBeGreaterThanOrEqual(2);await video.evaluate(v=>v.pause());
   await expect(page.locator('.pd-stat').filter({hasText:'销量'})).toHaveCount(on?1:0);
   if(!on)assert.equal((await page.evaluate(()=>window.salesSeen)).length,0,'hidden detail sales flashed');
   await page.goto(url+'/?sort=sales',{waitUntil:'domcontentloaded'});
   await expect(page.locator('.product-card')).toHaveCount(2);
   await expect(page.locator('.catalog-sort-select option[value="sales"]')).toHaveCount(on?1:0);
   await expect(page.locator('.catalog-sort-tabs button').filter({hasText:/^销量$/})).toHaveCount(on?1:0);
   await expect(page.locator('.pc-sales')).toHaveCount(on?2:0);
   if(!on){assert(!state.requests.includes('sales'),'closed sales sort was sent to API');await expect(page.locator('.catalog-sort-select')).toHaveValue('default');}
   else assert(state.requests.includes('sales'),'enabled sales sort did not work');
   if(!test.fail){await expect.poll(()=>page.locator('.app').evaluate(el=>getComputedStyle(el).backgroundImage)).toContain(test.width<768?'bg-mobile.svg':'bg.svg');await page.evaluate(()=>window.checkBackground=true);}
   // Repeated product entry and return must preserve the same policy.
   for(let i=0;i<2;i++){
    await page.locator('.product-card').nth(i).click();await expect.poll(()=>pending.size,{timeout:5000}).toBe(0).catch(e=>{console.log('pending requests', [...pending].map(r=>({url:r.url(),type:r.resourceType()})));throw e});
    await expect(page.locator('.pd-name')).toHaveText('测试商品'+(i+1));
    await expect(page.locator('#pd-reviews')).toHaveCount(on?1:0);if(!test.fail)assert.equal((await page.evaluate(()=>window.backgroundLost)).length,0,'background disappeared during navigation');
    await expect(page.locator('.pd-stat').filter({hasText:'销量'})).toHaveCount(on?1:0);
    if(i===0&&!test.fail)await page.screenshot({path:join(out,'product-'+test.name+'.png')});
    await page.goBack();await expect(page.locator('.product-card')).toHaveCount(2);
   }
   if(!on)assert.equal((await page.evaluate(()=>window.salesSeen)).length,0,'sales flashed during navigation');
   if(test.name==='off-mobile')await page.screenshot({path:join(out,'mobile-media.png')});
   await page.goto(url+'/posts/tutorial');await expect(page.locator('.post-title')).toHaveText('视频教程');
   const postVideo=page.locator('.post-body video');await expect(postVideo).toHaveCount(1);await expect(postVideo).toHaveAttribute('preload','none');
   await postVideo.evaluate(async v=>{v.muted=true;await v.play()});await expect.poll(()=>postVideo.evaluate(v=>v.readyState)).toBeGreaterThanOrEqual(2);
   await page.screenshot({path:join(out,'blog-'+test.name+'.png')});
   assert.equal(errors.length,0,errors.join('\n'));
   results.push({scenario:test.name,passed:true,sortRequests:[...new Set(state.requests)]});
   await ctx.close();
  }
  console.log(JSON.stringify(results,null,2));
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exit(1)});
