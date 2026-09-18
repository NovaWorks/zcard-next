/** Isolated production-build regression; no customer data or credentials.
 * PLAYWRIGHT_MODULE=/path/to/playwright/test node scripts/test_admin_data_loading.cjs
 */
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE||'@playwright/test');
const root=path.resolve(__dirname,'../admin/dist');
const server=http.createServer((req,res)=>{
 let file=path.join(root,new URL(req.url,'http://localhost').pathname.replace(/^\/admin\/?/,''));
 if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');
 res.setHeader('Content-Type',({'.html':'text/html','.js':'application/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');
 res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
 try{
  const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],requests=[];
  let releaseTraffic,releaseCharts,trafficStarted=false,chartStarted=false,failOptions=false,failTraffic=false;
  const trafficGate=new Promise(r=>releaseTraffic=r),chartGate=new Promise(r=>releaseCharts=r);
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(()=>{localStorage.setItem('token',JSON.stringify('local-test'));localStorage.setItem('refreshToken',JSON.stringify('local-refresh'));});
  await page.route('**/echarts-runtime-*.js',async route=>{chartStarted=true;await chartGate;await route.continue();});
  await page.route('**/api/v1/**',async route=>{
   const u=new URL(route.request().url()),p=u.pathname;requests.push(u);
   let data={items:[],total:0,categories:[],orders:[],users:[],products:[],roles:[],trend:[],points:[],top_products:[],top_channels:[],pending:{},connections:[],batches:[]};
   if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'local-test'},permissions:['*']};
   if(p.endsWith('/captcha-config'))data={enabled:false};
   if(p.endsWith('/products')){
    if(u.searchParams.get('options_only')==='true'){
     const n=Number(u.searchParams.get('page'));
     if(failOptions && n===2)return route.fulfill({status:500,json:{code:500,reason:'fixture.ERROR',message:'fixture failure'}});
     // Simulate an older server limiting pages to 100 despite a 500 request.
     data={products:Array.from({length:n===1?100:7},(_,i)=>({id:(n-1)*100+i+1,name:'选择商品-'+((n-1)*100+i+1),price_cents:100,stock_type:'card'})),page:n,page_size:100,total:107};
    }else data={products:[{id:1,name:'回归商品',price_cents:100,stock_type:'card',status:1}],total:1};
   }
   if(p.endsWith('/dashboard'))data={...data,today:{revenue:12345},trend:[{date:'2026-09-19',revenue:12345,orders:1}]};
   if(p.endsWith('/dashboard/traffic')){trafficStarted=true;await trafficGate;if(failTraffic)return route.fulfill({status:500,json:{code:500,reason:'fixture.ERROR',message:'traffic unavailable'}});}
   await route.fulfill({json:data});
  });
  await page.goto(baseUrl()+'/admin/home');
  await expect.poll(()=>trafficStarted&&chartStarted).toBe(true);
  await expect(page.locator('main')).toContainText('123.45');
  await expect(page.locator('#nprogress')).toHaveCount(0);
  await expect(page.locator('main .n-spin-container').first()).not.toHaveClass(/n-spin-container--blur/);
  assert.equal(requests.filter(u=>u.pathname.endsWith('/dashboard')).length,1);
  console.log('PASS dashboard data and route ready while traffic and chart code are held');
  releaseCharts();releaseTraffic();
  await expect(page.locator('main canvas').first()).toBeVisible();
  await expect(page.locator('main .n-spin-body')).toHaveCount(0);
  await page.getByRole('menuitem',{name:'商品管理',exact:true}).click();
  await expect(page.locator('main')).toContainText('回归商品');
  assert.equal(requests.filter(u=>u.pathname.endsWith('/reviews')).length,0,'hidden drawer requested reviews');
  await page.getByRole('button',{name:'评价',exact:true}).click();
  await expect.poll(()=>requests.filter(u=>u.pathname.endsWith('/reviews')).length).toBe(1);
  await page.keyboard.press('Escape');
  await page.getByRole('menuitem',{name:'卡密库存',exact:true}).click();
  await expect.poll(()=>requests.filter(u=>u.pathname.endsWith('/products')&&u.searchParams.get('options_only')==='true').length).toBe(2);
  const picker=page.locator('main .n-select').nth(1);
  await picker.click();await picker.locator('input').fill('选择商品-107');
  await expect(page.locator('.n-base-select-option')).toContainText('选择商品-107');
  await page.keyboard.press('Escape');
  console.log('PASS unopened reviews skipped and product 107 searchable across capped pages');
  failOptions=true;await page.reload();
  await expect(page.locator('main')).toContainText('商品选项加载失败');
  failOptions=false;await page.getByRole('button',{name:'重新加载',exact:true}).click();
  await expect(page.locator('main')).not.toContainText('商品选项加载失败');
  await picker.click();await picker.locator('input').fill('选择商品-107');
  await expect(page.locator('.n-base-select-option')).toContainText('选择商品-107');
  await page.keyboard.press('Escape');
  failTraffic=true;await page.getByRole('menuitem',{name:'首页',exact:true}).click();
  await expect(page.locator('main')).toContainText('123.45');
  await expect(page.locator('main .n-spin-body')).toHaveCount(0);
  assert.deepEqual(errors,[]);
  console.log('PASS option retry, traffic failure recovery and no runtime errors');
  function baseUrl(){return 'http://127.0.0.1:'+server.address().port;}
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
