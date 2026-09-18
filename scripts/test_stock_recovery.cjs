/** Stock recovery browser regression with disposable local fixtures.
 * PLAYWRIGHT_MODULE=/path/to/playwright/test node scripts/test_stock_recovery.cjs
 * Rebuilds storefront/dist; release builds replace the fixture output.
 */
const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const {createServer}=require('node:http');
const {readFileSync,existsSync,writeFileSync,mkdtempSync}=require('node:fs');
const {join,extname,resolve}=require('node:path');
const {spawn}=require('node:child_process');
const assert=require('node:assert/strict');
const out=mkdtempSync(join(require('node:os').tmpdir(),'zcard-stock-browser-'));
const project=resolve(__dirname,'../storefront');const root=join(project,'dist');
const product=(id,stock,status='current')=>({id,name:'库存测试'+id,slug:'stock-'+id,description:'库存恢复测试',price_cents:1620,stock_type:'card',stock,stock_status:status,stock_reference:stock,stock_visible:true,category_id:1,controls:[],skus:[]});
let recovered=false,detailRecovered=false,listCalls=0,detailCalls=0;
const items=()=>[product(1,recovered?37:-2,recovered?'current':'unknown'),product(2,0),product(3,-1)];
const server=createServer((req,res)=>{
 const u=new URL(req.url,'http://localhost');const p=u.pathname;
 res.setHeader('Cache-Control','no-store');
 if(p.startsWith('/api/')){
  res.setHeader('Content-Type','application/json');
  let data={items:[],posts:[],banners:[],currencies:[],total:0};
  if(p.endsWith('/config'))data={entries:Object.entries({'site.name':'库存测试商店','trade.cart_enabled':false,'template.show_stock':true,'template.default_view':'list'}).map(([key,value])=>({key,value_json:JSON.stringify(value)}))};
  if(p==='/api/v1/storefront/products'){listCalls++;data={items:items(),total:3,page:1,page_size:20};}
  if(/^\/api\/v1\/storefront\/products\/\d+$/.test(p)){
   detailCalls++;const id=Number(p.split('/').pop());
   data=id===1?{...product(1,detailRecovered?37:-2,detailRecovered?'current':'stale'),stock_reference:37,stock_checked_at:1789732800}:items()[id-1];
  }
  if(p.endsWith('/categories'))data={categories:[{id:1,name:'测试分类'}]};
  return res.end(JSON.stringify(data));
 }
 let file=join(root,p==='/'?'index.html':p);
 if(!existsSync(file)&&!extname(p))file=join(root,p+'.html');
 if(!existsSync(file)){res.statusCode=404;return res.end();}
 res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.png':'image/png','.svg':'image/svg+xml'})[extname(file)]||'application/octet-stream');
 let content=readFileSync(file);
 if(extname(file)==='.html')content=content.toString().replace('<head>','<head><script type="application/json" id="zcard-theme-runtime">'+JSON.stringify({key:'classic',values:{},capabilities:{cart:false},branding:{name:'库存测试商店',logo:''}})+'</script>');
 res.end(content);
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const url='http://127.0.0.1:'+server.address().port;
 let log='';const build=spawn('pnpm',['build'],{cwd:project,env:{...process.env,VITE_SSG_API:url}});build.stdout.on('data',d=>log+=d);build.stderr.on('data',d=>log+=d);
 assert.equal(await new Promise(r=>build.on('exit',r)),0,log);writeFileSync(join(out,'build.log'),log);
 const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
 try{
  for(const width of [390,1280]){
   recovered=false;detailRecovered=false;
   const ctx=await browser.newContext({viewport:{width,height:900}});const page=await ctx.newPage();const errors=[];
   page.on('pageerror',e=>errors.push(e.message));
   await page.clock.install();
   await page.goto(url+'/',{waitUntil:'networkidle'});
   await expect(page.locator('.product-card')).toHaveCount(3);
   await expect(page.locator('.product-card').first()).toContainText('库存待确认');
   await expect(page.locator('.product-card').first()).toContainText('查看详情');
   await expect(page.locator('.product-card').nth(1)).toContainText('暂时售罄');
   await expect(page.locator('.product-card').nth(2)).toContainText('不限库存');
   const before=listCalls;recovered=true;
   await page.clock.fastForward(16000);
   await expect(page.locator('.product-card').first()).toContainText('库存 37');
   assert(listCalls>before,'list did not refresh unknown stock');
   await page.locator('.product-card').first().click();
   await expect(page.locator('.pd-name')).toHaveText('库存测试1');
   await expect(page.locator('.pd-stat').filter({hasText:'库存'})).toContainText('上次：37（待确认）');
   await expect(page.locator('.pd-btn-buy')).toBeDisabled();
   const password=page.getByPlaceholder('用于取货验证（忘记将无法取货）');await password.fill('keep1234');
   await page.locator('.pd-stock-notice button').click();
   await expect(page.locator('.pd-stock-notice button')).toBeDisabled();
   await expect(page.locator('.pd-stock-notice')).toContainText('仍未获取到有效库存');
   const failedCalls=detailCalls;await page.clock.fastForward(16000);detailRecovered=true;
   await page.locator('.pd-stock-notice button').click();
   await expect(page.locator('.pd-stock-notice')).toHaveCount(0);
   await expect(page.locator('.pd-btn-buy')).toBeEnabled();
   await expect(password).toHaveValue('keep1234');assert(detailCalls>failedCalls);
   await expect(page.locator('.pd-stat').filter({hasText:'库存'})).toContainText('37');
   await page.screenshot({path:join(out,'recovered-'+width+'.png'),fullPage:true});
   assert.deepEqual(errors,[]);console.log('PASS stock recovery, width='+width);
   await ctx.close();
  }
 }finally{await browser.close();server.close();}
 console.log('Artifacts: '+out);
})().catch(e=>{console.error(e);server.close();process.exit(1)});
