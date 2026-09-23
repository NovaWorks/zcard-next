// Isolated admin build fixtures, never real orders or users.
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE||'@playwright/test');
const root=path.resolve(__dirname,'../admin/dist'),output='/tmp/zcard-manual-services';fs.mkdirSync(output,{recursive:true});
const server=http.createServer((req,res)=>{let file=path.join(root,new URL(req.url,'http://localhost').pathname.replace(/^\/admin\/?/,''));if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');res.setHeader('Content-Type',({'.html':'text/html','.js':'application/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');res.end(fs.readFileSync(file));});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true});try{
 for(const width of [1440,390]) {
  const page=await browser.newPage({viewport:{width,height:1000}}),errors=[];let status='pending',submitted;
  page.on('pageerror',e=>errors.push(e.message));await page.addInitScript(()=>{localStorage.setItem('token',JSON.stringify('local-test'));localStorage.setItem('refreshToken',JSON.stringify('local-refresh'));});
  await page.route('**/api/v1/**',async route=>{
   const p=new URL(route.request().url()).pathname,req=route.request();let data={items:[],total:0,categories:[],orders:[],users:[],products:[],roles:[],levels:[],pending:{}};
   const item={id:11,order_item_id:11,order_no:'MANUAL-LOCAL',product_id:1,name:'频道服务',product_name:'频道服务',quantity:1,unit_price_cents:600,amount_cents:600,fulfillment_type:'manual',fulfillment_status:status,form_answers_json:JSON.stringify([{name:'频道链接',value:'https://t.me/local_fixture'}])};
   if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'local-test'},permissions:['*']};
   if(p.endsWith('/captcha-config'))data={enabled:false};
   if(p.endsWith('/orders'))data={orders:[{order_no:'MANUAL-LOCAL',status:'fulfilling',total_cents:600}],total:1};
   if(p.endsWith('/fulfillment/pending'))data={orders:status==='delivered'?[]:[item]};
   if(p.endsWith('/orders/MANUAL-LOCAL'))data={order_no:'MANUAL-LOCAL',status:'fulfilling',total_cents:600,items:[item]};
   if(p.endsWith('/MANUAL-LOCAL/start')){assert.equal(req.postDataJSON().order_item_id,11);status='delivering';data={};}
   if(p.endsWith('/MANUAL-LOCAL/deliver')){submitted=req.postDataJSON();status='delivered';data={};}
   await route.fulfill({json:data});
  });
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/order`);await page.getByText('待发货',{exact:true}).click();
  await page.getByRole('button',{name:'手动发货',exact:true}).click();await expect(page.getByText('https://t.me/local_fixture',{exact:true})).toBeVisible();
  const finish=page.getByRole('button',{name:'确认完成',exact:true});await expect(finish).toBeDisabled();
  await page.getByRole('button',{name:'开始处理',exact:true}).click();await expect(page.getByRole('button',{name:'开始处理',exact:true})).toHaveCount(0);await expect(finish).toBeDisabled();
  await page.getByPlaceholder('如：已完成开通；能量交付可附交易编号').fill('本地验证：频道服务已完成');await expect(finish).toBeEnabled();
  await page.screenshot({path:path.join(output,`admin-service-${width}.png`),fullPage:true});
  await finish.click();await expect.poll(()=>submitted?.service_content).toBe('本地验证：频道服务已完成');assert.equal(submitted.order_item_id,11);assert(!submitted.content);assert.deepEqual(errors,[]);await page.close();console.log(`admin manual services ${width}px: passed`);
 }
 }finally{await browser.close();server.close();}})().catch(e=>{console.error(e);server.close();process.exitCode=1});
