// Isolated production-build UI fixtures. No real suppliers or Telegram calls.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const out='/tmp/zcard-low-stock-ui';fs.mkdirSync(out,{recursive:true});
const root=path.resolve(__dirname,'../admin/dist');
const server=http.createServer((req,res)=>{let file=path.join(root,new URL(req.url,'http://local').pathname.replace(/^\/admin\/?/,''));if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');res.setHeader('Content-Type',({'.html':'text/html','.js':'application/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base=`http://127.0.0.1:${server.address().port}`;
 const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
 try{for(const width of [1440,390]){
  const page=await browser.newPage({viewport:{width,height:1000}}),errors=[];let scheduleSaved,settingsSaved;
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(()=>{localStorage.setItem('token',JSON.stringify('fixture'));localStorage.setItem('refreshToken',JSON.stringify('fixture'));});
  const connection={id:1,name:'本地测试货源',driver:'zcard',status:'active',base_url:'https://example.invalid',credentials_set:true,settings:JSON.stringify({schedule:{enabled:false,low_stock:{enabled:false,threshold:10,interval:5}}}),low_stock_scanned_at:Math.floor(Date.now()/1000),low_stock_message:'本轮检查 2 个规格，0 个待确认'};
  const notifyValues={telegram_enabled:true,telegram_order_enabled:false,telegram_low_stock_enabled:false,telegram_bot_token:'****',telegram_targets:[{chat_id:'123',topic_id:7}],telegram_events:['order.paid']};
  await page.route('**/api/v1/**',async route=>{
   const req=route.request(),url=new URL(req.url()),p=url.pathname;let data={items:[],total:0,connections:[],roles:[],pending:{}};
   if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'fixture'},permissions:['*']};
   if(p.endsWith('/supply/connections'))data={connections:[connection],total:1};
   if(p.endsWith('/supply/connections/1')&&req.method()==='PUT'){scheduleSaved=JSON.parse(req.postDataJSON().settings);connection.settings=JSON.stringify(scheduleSaved);data=connection;}
   if(p.endsWith('/settings')){
    if(req.method()==='PUT'){settingsSaved=req.postDataJSON();data={updated:10};}
    else if(url.searchParams.get('group')==='notify')data={items:Object.entries(notifyValues).map(([key,v])=>({group:'notify',key,value_json:JSON.stringify(v)}))};
    else if(url.searchParams.get('group')==='supply')data={items:[{group:'supply',key:'low_stock_alert_enabled',label:'启用库存预警',value_json:'true'},{group:'supply',key:'low_stock_threshold',label:'库存低于此数量时提醒',value_json:'5'}]};
   }
   if(p.endsWith('/notify/logs'))data={logs:[],total:0};
   await route.fulfill({json:data});
  });
  await page.goto(base+'/admin/channel');
  await page.getByRole('button',{name:width===390?'操作 ▾':'更多 ▾',exact:true}).click();
  await page.getByText('⏰ 定时计划',{exact:true}).click();
  await page.getByRole('switch',{name:'低库存加速检查',exact:true}).click();
  await page.getByLabel('加速检查库存阈值',{exact:true}).fill('12');
  await page.getByLabel('低库存检查间隔',{exact:true}).fill('7');
  const dialog=page.locator('.n-modal');await dialog.getByText('本轮检查 2 个规格，0 个待确认',{exact:true}).waitFor();
  await page.screenshot({path:path.join(out,`schedule-${width}.png`),fullPage:true,animations:'disabled'});
  const box=await dialog.boundingBox();assert(box.x>=0&&box.x+box.width<=width,'schedule overflows');
  await page.getByRole('button',{name:'保存计划',exact:true}).click();
  await dialog.waitFor({state:'hidden'});
  assert.equal(scheduleSaved.schedule.enabled,false);assert.deepEqual(scheduleSaved.schedule.low_stock,{enabled:true,threshold:12,interval:7});
  await page.goto(base+'/admin/settings');
  await page.getByText('货源',{exact:true}).click();
  await page.getByLabel('库存低于此数量时提醒',{exact:true}).fill('8');
  await page.getByLabel('库存低于此数量时提醒',{exact:true}).press('Tab');
  await page.getByRole('button',{name:'保存更改',exact:true}).click();
  await page.getByText('设置已保存',{exact:true}).waitFor();
  assert(settingsSaved.items.some(i=>i.key==='low_stock_threshold'&&i.value_json==='8'));
  const thresholdBox=await page.getByLabel('库存低于此数量时提醒',{exact:true}).boundingBox();assert(thresholdBox.x>=0&&thresholdBox.x+thresholdBox.width<=width,'threshold input overflows');
  const thresholdLabel=await page.getByText('库存低于此数量时提醒',{exact:true}).boundingBox();assert(thresholdLabel.x>=0&&thresholdLabel.x+thresholdLabel.width<=width,'threshold label overflows');
  await page.screenshot({path:path.join(out,`threshold-${width}.png`),fullPage:true,animations:'disabled'});
  await page.getByText('工单 / TG通知',{exact:true}).click();
  await page.getByRole('switch',{name:'主站低库存通知',exact:true}).click();
  assert.equal(await page.getByRole('switch',{name:'主站订单通知',exact:true}).getAttribute('aria-checked'),'false');
  await page.getByRole('button',{name:'保存 TG 配置',exact:true}).click();
  await page.getByText('TG通知设置已保存',{exact:true}).waitFor();
  assert(settingsSaved.items.some(i=>i.key==='telegram_low_stock_enabled'&&i.value_json==='true'));
  assert(settingsSaved.items.some(i=>i.key==='telegram_order_enabled'&&i.value_json==='false'));
  await page.getByRole('switch',{name:'主站低库存通知',exact:true}).scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(out,`telegram-${width}.png`),fullPage:true,animations:'disabled'});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'settings overflow');
  assert.deepEqual(errors,[]);await page.close();console.log(`low-stock UI ${width}px: passed`);
 }}finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
