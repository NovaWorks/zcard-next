const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE);
const repo=process.env.ZCARD_CHECK_ROOT||path.resolve(__dirname,'..');
const root=path.join(repo,'admin/dist');
const server=http.createServer((req,res)=>{let f=path.join(root,new URL(req.url,'http://localhost').pathname.replace(/^\/admin\/?/,''));if(!fs.existsSync(f)||fs.statSync(f).isDirectory())f=path.join(root,'index.html');res.setHeader('Content-Type',({'.html':'text/html','.js':'application/javascript','.css':'text/css'})[path.extname(f)]||'application/octet-stream');res.end(fs.readFileSync(f));});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH});try{for(const width of [1440,390]){
 const page=await browser.newPage({viewport:{width,height:1000}});await page.addInitScript(()=>localStorage.setItem('token',JSON.stringify('fixture')));const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/api/v1/**',async route=>{let data={items:[],total:0,categories:[],products:[],connections:[]};const p=new URL(route.request().url()).pathname;
 if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'fixture'},permissions:['*']};
 if(p.endsWith('/captcha-config'))data={enabled:false};
 if(p.endsWith('/supply/connections'))data={connections:[{id:1,name:'私有版上游',driver:'zcard',base_url:'https://upstream.invalid',status:'active',exchange_rate:1,settings:'{}'}],total:1};
 if(p.endsWith('/preview'))data={snapshot_id:'fixture',status:'ready',total:1,categories:[{code:'1',name:'短信接码',products:[{code:'42',name:'接码服务',product_kind:'sms_channel',delivery_kind:'sms_activation',price_cents:0,stock:-2,quote_status:'ready',is_active:true}]}]};
 await route.fulfill({json:data});});
 await page.goto(`http://127.0.0.1:${server.address().port}/admin/channel`);
 if(width===1440){await expect(page.getByRole('button',{name:'导入商品',exact:true})).toBeVisible();await page.getByRole('button',{name:'导入商品',exact:true}).click();}else{await page.getByRole('button',{name:/^操作/}).first().click();await page.getByText('导入商品',{exact:true}).last().click();}
 const modal=page.locator('.n-modal').filter({hasText:'导入上游商品'});await expect(modal).toBeVisible();await expect(modal.locator('.product-item')).toHaveCount(1);await expect(modal.getByRole('checkbox',{name:/接码服务/})).toBeChecked();await expect(modal.locator('.product-item')).toContainText(/实时报价|实时定价/);assert.deepEqual(errors,[]);await page.screenshot({path:`/tmp/${path.basename(repo)}-import-entry-${width}.png`,fullPage:true,animations:'disabled'});await page.close();console.log(JSON.stringify({width,importVisible:true,channelProducts:1}));
}}finally{await browser.close();await new Promise(r=>server.close(r));}})().catch(e=>{console.error(e);server.close();process.exitCode=1});
