// Actual built UI against disposable fixtures; no customer data or purchases.
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE||'@playwright/test');
const root=path.resolve(__dirname,'../admin/dist');
const server=http.createServer((req,res)=>{let f=path.join(root,new URL(req.url,'http://localhost').pathname.replace(/^\/admin\/?/,''));if(!fs.existsSync(f)||fs.statSync(f).isDirectory())f=path.join(root,'index.html');res.setHeader('Content-Type',({'.html':'text/html','.js':'application/javascript','.css':'text/css'})[path.extname(f)]||'application/octet-stream');res.end(fs.readFileSync(f));});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});try{
 for(const width of [1440,390]){
  const page=await browser.newPage({viewport:{width,height:1000}});let removed=false,attempts=0;const errors=[];
  page.on('pageerror',e=>errors.push(e.message));await page.addInitScript(()=>localStorage.setItem('token',JSON.stringify('fixture')));
  await page.route('**/api/v1/**',async route=>{const r=route.request(),url=new URL(r.url()),p=url.pathname;let data={products:[],total:0,items:[],categories:[],connections:[]};
   if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'fixture'},permissions:['*']};
   if(p.endsWith('/captcha-config'))data={enabled:false};
   if(p.endsWith('/admin/products'))data={products:removed?[]:[{id:1,name:'锁定商品',price_cents:100,status:1,is_locked:true,stock_type:'card',goods_type:'virtual'}],total:removed?0:1};
   if(p.endsWith('/delete-preview'))data={name:'锁定商品',order_count:2};
   if(r.method()==='DELETE'&&p.endsWith('/products/1')){assert(!url.searchParams.has('delete_orders'));attempts++;if(attempts===1)return route.fulfill({status:503,json:{code:503,message:'临时失败，请重试'}});removed=true;data={};}
   await route.fulfill({json:data}).catch(()=>{});
  });
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/product`);
  const remove=page.getByRole('button',{name:'删除',exact:true});await expect(remove).toBeEnabled();await remove.click();
  const modal=page.locator('.n-modal').filter({hasText:'删除商品'});await expect(modal.getByText('保留关联订单：')).toBeVisible();await expect(modal.getByText('2',{exact:true})).toBeVisible();
  await expect(modal.getByRole('button',{name:'删除商品及关联订单'})).toHaveCount(0);
  const confirm=modal.getByRole('button',{name:'确认删除（保留订单）',exact:true});await expect(confirm).toBeEnabled();await confirm.click();await expect(confirm).toBeEnabled();assert.equal(removed,false);await confirm.click();await expect(modal).toHaveCount(0);await expect(remove).toHaveCount(0);assert.equal(attempts,2);
  assert.deepEqual(errors,[]);await page.close();console.log(JSON.stringify({width,lockedDelete:true,preserveHistory:true,retry:true}));
 }
}finally{await browser.close();await new Promise(r=>server.close(r));}})().catch(e=>{console.error(e);server.close();process.exitCode=1});
