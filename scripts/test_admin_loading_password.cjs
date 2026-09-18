/** Run against a production admin build with local API fixtures only.
 * PLAYWRIGHT_MODULE=/path/to/playwright/test node scripts/test_admin_loading_password.cjs
 */
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE||'@playwright/test');
const root=path.resolve(__dirname,'../admin/dist');
const out=fs.mkdtempSync(path.join(require('node:os').tmpdir(),'zcard-admin-loading-'));
const server=http.createServer((req,res)=>{
 let file=path.join(root,new URL(req.url,'http://localhost').pathname.replace(/^\/admin\/?/,''));
 if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');
 res.setHeader('Content-Type',({'.html':'text/html','.js':'application/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png'})[path.extname(file)]||'application/octet-stream');
 res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
 try{
  const page=await browser.newPage({viewport:{width:1440,height:1000}});const errors=[],requests=[],assets=[];let passwordRequests=0;
  page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(r.url().endsWith('.js'))assets.push(r.url());});
  await page.addInitScript(()=>{localStorage.setItem('token',JSON.stringify('local-test'));localStorage.setItem('refreshToken',JSON.stringify('local-refresh'));});
  await page.route('**/api/v1/**',async route=>{
   const p=new URL(route.request().url()).pathname;requests.push(p);
   if(p.endsWith('/auth/password')){
    passwordRequests++;const body=route.request().postDataJSON();
    assert.equal(body.new_password,'newpass456');assert.equal(body.confirm_password,'newpass456');
    if(body.current_password!=='oldpass123')return route.fulfill({status:400,json:{code:400,reason:'identity.CURRENT_PASSWORD_INVALID',message:'当前密码不正确'}});
    return route.fulfill({json:{}});
   }
   let data={items:[],total:0,entries:[],categories:[],currencies:[],orders:[],users:[],products:[],admins:[],logs:[],roles:[],templates:[],trend:[],points:[],top_products:[],top_channels:[],pending:{},connections:[],tickets:[],posts:[]};
   if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'local-test'},permissions:['*']};
   if(p.endsWith('/captcha-config'))data={enabled:false};
   // API latency must leave the route shell responsive; route assets must not
   // include unopened editors/import dialogs/inactive channel tabs.
   await new Promise(r=>setTimeout(r,80));await route.fulfill({json:data});
  });
  await page.goto(base+'/admin/user');await expect(page.locator('main')).toContainText('用户管理');
  async function nav(label){
   const at=Date.now();await page.getByRole('menuitem',{name:label,exact:true}).click();
   await expect(page.locator('main')).toContainText(label);
   await expect(page.locator('#nprogress')).toHaveCount(0);
   await expect.poll(()=>page.locator('main').evaluate(el=>[...el.querySelectorAll('*')].some(n=>/(?:enter|leave)-active/.test(n.className||'')))).toBe(false);
   console.log('PASS route '+label+' shell '+(Date.now()-at)+'ms');
  }
  for(const label of ['商品管理','内容管理','工单管理','渠道管理'])await nav(label);
  assert.equal(requests.filter(p=>p==='/api/v1/admin/tickets').length,1,'ticket list loaded twice on first entry');
  assert(!assets.some(u=>/rich-editor-impl-|md-html-editor-impl-|import-modal-|supplier-accounts-tab-|procurement-tab-/.test(u)),'unopened components eagerly loaded');
  await page.locator('.n-tabs-tab').filter({hasText:'供货账号'}).click();
  await expect.poll(()=>assets.some(u=>/supplier-accounts-tab-/.test(u))).toBe(true);
  await nav('内容管理');await page.getByRole('button',{name:'新增文章',exact:true}).click();
  await expect(page.locator('.w-e-text-container [contenteditable=true]')).toBeVisible({timeout:15000});
  await page.locator('.w-e-text-container [contenteditable=true]').fill('编辑器按需加载正常');
  await page.keyboard.press('Escape');await expect(page.locator('.w-e-text-container')).toHaveCount(0);
  await nav('工单管理');await expect.poll(()=>requests.filter(p=>p==='/api/v1/admin/tickets').length).toBe(2);
  // Both placements must fit naturally without a carousel or clipped links.
  for(const width of [390,1024,1440]){
   await page.setViewportSize({width,height:800});
   for(const route of ['home','payment-channel']){
    await page.goto(base+'/admin/'+route);
    const sponsors=page.locator('main .sponsor-slots');
    await expect(sponsors.locator('a')).toHaveCount(2);
    for(const link of await sponsors.locator('a').all()){
     await expect(link).toHaveAttribute('href','https://t.me/ZhonCard');
     await expect(link).toBeInViewport({ratio:1});
    }
    const box=await sponsors.boundingBox();assert(box.x>=0 && box.x+box.width<=width,'sponsors overflow viewport');
    if(route==='payment-channel'){
     const add=page.locator('.payment-add');await expect(add).toBeVisible();
     if(width===1440){const button=await add.boundingBox();assert(box.x+box.width<=button.x,'sponsors overlap add button');}
    }
    await expect(page.locator('#nprogress')).toHaveCount(0);
    await expect(page.locator('main .n-spin-body')).toHaveCount(0);
    await page.screenshot({path:path.join(out,route+'-'+width+'.png')});
   }
  }
  await page.setViewportSize({width:1440,height:1000});
  await page.getByText('local-test',{exact:true}).click();
  const labels=await page.locator('.n-dropdown-option-body__label').allTextContents();
  assert(labels.indexOf('修改密码')===labels.indexOf('账号安全')+1,'password menu is not below account security');
  await page.getByText('修改密码',{exact:true}).click();
  const dialog=page.locator('.n-modal').filter({hasText:'修改密码'});
  await dialog.getByLabel('当前密码',{exact:true}).fill('incorrect');
  await dialog.getByLabel('新密码',{exact:true}).fill('newpass456');
  await dialog.getByLabel('确认新密码',{exact:true}).fill('mismatch');
  await dialog.getByRole('button',{name:'确认修改',exact:true}).click();
  await expect(dialog).toContainText('两次输入的新密码不一致');assert.equal(passwordRequests,0);
  await dialog.getByLabel('确认新密码',{exact:true}).fill('newpass456');
  await dialog.getByRole('button',{name:'确认修改',exact:true}).click();
  await expect(dialog).toContainText('当前密码不正确');assert.equal(passwordRequests,1);
  assert(await page.evaluate(()=>localStorage.getItem('token')),'wrong current password logged user out');
  await dialog.getByLabel('当前密码',{exact:true}).fill('oldpass123');
  await dialog.getByRole('button',{name:'确认修改',exact:true}).click();
  await expect(page).toHaveURL(/\/login/);
  assert.equal(await page.evaluate(()=>localStorage.getItem('token')),null);
  assert.equal(await page.evaluate(()=>localStorage.getItem('refreshToken')),null);
  assert.equal(passwordRequests,2);assert.deepEqual(errors,[]);
  await page.screenshot({path:path.join(out,'logged-out.png')});
  console.log('PASS lazy editors/tabs, ticket refresh, password confirmation, error recovery and automatic logout');
  console.log('Artifacts: '+out);
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
