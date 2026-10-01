// Built admin UI + disposable in-memory API fixture. Never modifies production.
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE||'@playwright/test');
const root=path.resolve(__dirname,'../admin/dist');
const server=http.createServer((req,res)=>{
 const p=new URL(req.url,'http://localhost').pathname.replace(/^\/admin\/?/,'');
 let f=path.join(root,p);if(!fs.existsSync(f)||fs.statSync(f).isDirectory())f=path.join(root,'index.html');
 res.setHeader('Content-Type',({'.html':'text/html','.js':'application/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(f)]||'application/octet-stream');res.end(fs.readFileSync(f));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base=`http://127.0.0.1:${server.address().port}`;
 const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
 try{
 for(const width of [1440,390]){
  const page=await browser.newPage({viewport:{width,height:1000}}),errors=[],deletes=[];page.setDefaultTimeout(10000);
  page.on('pageerror',e=>errors.push(e.message));await page.addInitScript(()=>localStorage.setItem('token',JSON.stringify('fixture')));
  let permissions=['*'], categories=[{id:1,name:'空父类',sort:0},{id:2,name:'空子类',parent_id:1,sort:0},{id:3,name:'保留父类',sort:1},{id:4,name:'保留子类',parent_id:3,sort:0},{id:5,name:'有商品',product_count:1,sort:2},{id:6,name:'空分类',sort:3},{id:7,name:'网络重试',sort:4},{id:8,name:'响应丢失',sort:5},{id:9,name:'保留分类',sort:6}],retry=true;
  await page.route('**/api/v1/**',async route=>{
   const req=route.request(),p=new URL(req.url()).pathname;let data={items:[],total:0,categories:[],products:[],skus:[],controls:[]};
   if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'fixture'},permissions};
   if(p.endsWith('/captcha-config'))data={enabled:false};
   if(p.endsWith('/categories'))data={categories};
   if(req.method()==='DELETE'&&/\/categories\/\d+$/.test(p)){
    const id=Number(p.split('/').at(-1));deletes.push(id);
    await new Promise(r=>setTimeout(r,120));
    if(id===8){categories=categories.filter(c=>c.id!==id);return route.abort('failed');}
    if(id===7&&retry){retry=false;return route.fulfill({status:503,json:{code:503,message:'连接失败，请重试'}});}
    const removed=new Set([id]);for(const c of categories)if(removed.has(c.parent_id))removed.add(c.id);
    categories=categories.filter(c=>!removed.has(c.id));data={};
   }
   await route.fulfill({json:data}).catch(()=>{});
  });
  const select=(name)=>page.getByRole('checkbox',{name:`选择分类 ${name}`,exact:true});
  const action=()=>page.getByRole('button',{name:/^批量删除/});
  const search=page.getByPlaceholder('搜索分类名称或完整路径',{exact:true});
  const selectAll=page.getByRole('checkbox',{name:'全选当前筛选结果',exact:true});
  await page.goto(base+'/admin/category');await expect(select('空父类')).toBeVisible();await expect(action()).toBeDisabled();
  // Select-all applies only to the search result and preserves hidden selections.
  await search.fill('空');await selectAll.check();await expect(page.getByText('已选 3',{exact:true})).toBeVisible();
  await search.fill('有商品');await selectAll.check();await expect(page.getByText('已选 4',{exact:true})).toBeVisible();
  await selectAll.uncheck();await expect(page.getByText('已选 3',{exact:true})).toBeVisible();
  await search.fill('');await expect(select('空父类')).toBeChecked();await expect(select('有商品')).not.toBeChecked();
  // Cancel the destructive confirmation without submitting any requests.
  await action().click();await page.getByRole('button',{name:'取消',exact:true}).last().click();assert.equal(deletes.length,0);
  await select('保留父类').check();await select('有商品').check();await select('网络重试').check();await select('响应丢失').check();
  await action().click();await page.getByRole('button',{name:'确认删除',exact:true}).click();
  await expect(search).toBeDisabled();await expect(selectAll).toBeDisabled();
  await expect(page.locator('.category-delete-failures')).toContainText('连接失败，请重试');
  await expect(search).toBeEnabled();
  assert.deepEqual(deletes,[1,3,5,6,7,8],'selected parents must include descendants without duplicate requests');
  await expect(select('网络重试')).toBeChecked();
  await expect(select('保留父类')).toHaveCount(0);await expect(select('保留父类 / 保留子类')).toHaveCount(0);await expect(select('有商品')).toHaveCount(0);await expect(select('保留分类')).not.toBeChecked();
  await expect(select('空父类')).toHaveCount(0);await expect(select('响应丢失')).toHaveCount(0);await expect(page.locator('.category-delete-failures')).not.toContainText('响应丢失');await expect(page.getByText('已选 1',{exact:true})).toBeVisible();
  // Retry only the failed request; successes and cascaded children stay removed.
  await action().click();await page.getByRole('button',{name:'确认删除',exact:true}).click();await expect(select('网络重试')).toHaveCount(0);await expect(search).toBeEnabled();
  assert.deepEqual(deletes,[1,3,5,6,7,8,7]);await expect(action()).toBeDisabled();
  await select('保留分类').check();await page.getByRole('button',{name:'清空选择',exact:true}).click();await expect(action()).toBeDisabled();await expect(page.locator('.category-delete-failures')).toHaveCount(0);await expect(select('保留分类')).not.toBeChecked();await expect(selectAll).not.toBeChecked();
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'mobile overflow');
  await page.screenshot({path:`/tmp/${path.basename(path.resolve(__dirname,'..'))}-category-multi-delete-${width}.png`,fullPage:true,animations:'disabled'});
  // Permission to delete alone must be sufficient; read-only cannot select/delete.
  permissions=['catalog:category_read','catalog:category_delete'];await page.reload();await expect(select('保留分类')).toBeVisible();await expect(page.getByRole('button',{name:'新建分类',exact:true})).toHaveCount(0);
  permissions=['catalog:category_read'];await page.reload();await expect(page.getByText('保留分类',{exact:true}).first()).toBeVisible();await expect(page.getByRole('checkbox')).toHaveCount(0);await expect(action()).toHaveCount(0);
  permissions=['*'];await page.goto(base+'/admin/product');await page.locator('.product-category-card').getByRole('button',{name:'管理',exact:true}).click();
  const modal=page.locator('.n-modal').filter({hasText:'分类管理'});await expect(modal.getByRole('checkbox',{name:'选择分类 保留分类',exact:true})).toBeVisible();
  assert.deepEqual(errors,[]);await page.close();console.log(JSON.stringify({width,filteredMultiSelect:true,partialDelete:true,cascade:true,parentDeduplicated:true,retainFailed:true,retry:true,permissions:true,reusedModal:true}));
 }
 }finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
