// Local build fixtures only; never sends a real payment or changes gateway settings.
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE||'@playwright/test');
const root=path.resolve(__dirname,'../admin/dist');
const server=http.createServer((req,res)=>{let f=path.join(root,new URL(req.url,'http://local').pathname.replace(/^\/admin\/?/,''));if(!fs.existsSync(f)||fs.statSync(f).isDirectory())f=path.join(root,'index.html');res.setHeader('Content-Type',({'.html':'text/html','.js':'application/javascript','.css':'text/css'})[path.extname(f)]||'application/octet-stream');res.end(fs.readFileSync(f));});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const b=await chromium.launch();try{for(const width of [1440,390]){
 const page=await b.newPage({viewport:{width,height:1100}});let saved,fallback=false,empty=false;const errors=[];
 page.on('pageerror',e=>errors.push(e.message));await page.addInitScript(()=>{localStorage.setItem('token',JSON.stringify('fixture'));localStorage.setItem('refreshToken',JSON.stringify('fixture'));});
 const networkOptions=[{label:'TRON',value:'tron'},{label:'Polygon',value:'polygon'}];
 const fields=[{key:'api_url',label:'网关地址',type:'text'},{key:'network',label:'网络',type:'select',multiple:true,dynamic:true,options:networkOptions},{key:'token',label:'支付代币',type:'select',multiple:true,dynamic:true,options:[{label:'TRX',value:'TRX'},{label:'USDT',value:'USDT'}]}];
 const channel={id:1,code:'ep',name:'EP fixture',driver:'epusdt',enabled:true,config_json:JSON.stringify({api_url:'https://gateway.invalid',network:['tron','polygon'],token:['TRX']}),methods_json:'[]'};
 await page.route('**/api/v1/**',async r=>{const req=r.request(),url=new URL(req.url()),p=url.pathname;let data={items:[],total:0};
 if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'fixture'},permissions:['*']};
 if(p.endsWith('/captcha-config'))data={enabled:false};
 if(p.endsWith('/payment/drivers'))data={drivers:[{code:'epusdt',name:'EPUSDT fixture',fields}]};
 if(p.endsWith('/payment/channels'))data={channels:[channel]};
 if(p.endsWith('/field-options')){const partial=JSON.parse(url.searchParams.get('config_json'));const networks=partial.network||[];const tokens=networks.length===1&&networks[0]==='polygon'?['USDC','USDT']:['TRX','USDT'];data={options:empty?[]:url.searchParams.get('field')==='network'?networkOptions:tokens.map(value=>({label:value,value})),fallback};}
 if(req.method()==='PUT'){saved=req.postDataJSON();Object.assign(channel,saved);data=channel;}
 await r.fulfill({json:data});});
 await page.goto(`http://127.0.0.1:${server.address().port}/admin/payment-channel`);
 const open=async()=>{await page.getByRole('button',{name:/^(配置|去配置)$/}).click();};await open();
 const modal=page.locator('.n-modal').filter({hasText:'配置「EP fixture」'});
 const codes=page.getByPlaceholder('标识（如 alipay）');
 await page.getByRole('button',{name:'按模板生成',exact:true}).click();await expect(codes).toHaveCount(1);await expect(codes).toHaveValue('trx-tron');
 await page.getByRole('button',{name:'添加方式',exact:true}).click();await expect(codes).toHaveCount(2);await expect(codes.nth(1)).toHaveValue('trx-tron-2');
 const rows=modal.locator('div.rounded-8px').filter({has:page.getByPlaceholder('标识（如 alipay）')});
 const second=rows.nth(1);await second.locator('.n-select').first().click();await page.locator('.n-base-select-option').getByText('Polygon',{exact:true}).click();
 await expect(second.getByText('收款代币',{exact:true})).toBeVisible();
 await second.locator('.n-select').nth(1).click();await expect(page.locator('.n-base-select-option').filter({hasText:'TRX'})).toHaveCount(0);
 await page.locator('.n-base-select-option').getByText('USDT',{exact:true}).click();
 await page.getByPlaceholder('标识（如 alipay）').nth(1).fill('usdt-polygon');await page.getByPlaceholder('方式名称（如 支付宝）').nth(1).fill('USDT · Polygon');
 await modal.getByRole('button',{name:/保存/}).last().click();await expect.poll(()=>!!saved).toBe(true);
 assert.deepEqual(JSON.parse(saved.methods_json).map(m=>m.params),[{network:'tron',token:'TRX'},{network:'polygon',token:'USDT'}]);
 await expect(modal).not.toBeVisible();await open();await expect(codes).toHaveCount(2);
 fallback=true;await page.getByRole('button',{name:'刷新可用资产',exact:true}).click();await expect(page.getByText(/未确认 EP 可用资产/).first()).toBeVisible();
 await page.getByRole('button',{name:'按模板生成',exact:true}).click();await expect(page.getByText(/未能确认 EP 可用资产/)).toBeVisible();await expect(codes).toHaveCount(2);
 fallback=false;empty=true;await page.getByRole('button',{name:'刷新可用资产',exact:true}).click();await expect(page.getByText(/未确认 EP 可用资产/)).toHaveCount(0);
 await page.getByRole('button',{name:'按模板生成',exact:true}).click();await expect(page.getByText(/所选链与代币没有可用组合/)).toBeVisible();await expect(codes).toHaveCount(2);
 assert.deepEqual(errors,[]);await page.screenshot({path:`/tmp/zcard-epusdt-assets-${width}.png`,fullPage:true});await page.close();console.log(`EPUSDT assets ${width}px: passed`);
 }}finally{await b.close();server.close();}})().catch(e=>{console.error(e);server.close();process.exitCode=1});
