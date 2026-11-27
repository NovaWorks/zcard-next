/** Isolated UI regression: all API calls are mocked; no Telegram messages sent. */
const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE||'@playwright/test');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const root=process.env.ZCARD_ADMIN_DIST||path.resolve(__dirname,'../admin/dist');
const server=http.createServer((req,res)=>{let file=path.join(root,new URL(req.url,'http://local').pathname.replace(/^\/admin\/?/,''));if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css'})[path.extname(file)]||'application/octet-stream');res.end(fs.readFileSync(file));});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({executablePath:process.env.CHROME_BIN});try{
 for(const width of [390,1440]){
  const page=await browser.newPage({viewport:{width,height:1000}});const errors=[];let saves=[],tests=0,ticketReads=0;
  page.on('pageerror',e=>errors.push(e.message));await page.addInitScript(()=>{localStorage.setItem('token',JSON.stringify('fixture'));localStorage.setItem('refreshToken',JSON.stringify('fixture'));});
  const initial={telegram_enabled:true,telegram_order_enabled:false,telegram_low_stock_enabled:false,telegram_ticket_enabled:false,telegram_ticket_events:['ticket.created','ticket.replied','ticket.urgent'],telegram_bot_token:'****',telegram_targets:[{chat_id:'-10011',topic_id:7}],telegram_events:['order.paid']};
  await page.route('**/api/v1/**',async route=>{const u=new URL(route.request().url()),p=u.pathname;let data={items:[],total:0};
   if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'test'},permissions:['*']};
   if(p.endsWith('/settings')){if(route.request().method()==='PUT'){saves.push(route.request().postDataJSON());data={updated:1};}else data={items:u.searchParams.get('group')==='notify'?Object.entries(initial).map(([key,value])=>({group:'notify',key,value_json:JSON.stringify(value),secret:key==='telegram_bot_token'})):[]};}
   if(p.endsWith('/notify/telegram/test')){tests++;data={log_ids:[]};}
   if(p.endsWith('/notify/logs'))data={logs:[],total:0};
   if(p.endsWith('/tickets/T-FROM-TG')){ticketReads++;data={ticket:{id:1,ticket_no:'T-FROM-TG',status:'open',priority:'normal',type:'aftersale'},messages:[]};}
   await route.fulfill({json:data});
  });
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/settings`);try { await page.getByText('工单 / TG通知',{exact:true}).click({timeout:10000}); } catch(e) { console.log('PAGE_DIAGNOSTIC',page.url(),(await page.locator('body').innerText()).slice(0,2500),errors); throw e; }
  await expect(page.getByRole('switch',{name:'主站工单通知',exact:true})).toBeVisible();assert.equal(tests,0);
  await page.getByRole('switch',{name:'主站工单通知',exact:true}).click();
  await expect(page.getByRole('checkbox',{name:'新工单',exact:true})).toBeChecked();
  for(const name of ['新工单','客户追加回复','工单加急'])await page.getByRole('checkbox',{name,exact:true}).uncheck();
  await expect(page.getByRole('button',{name:'保存 TG 配置',exact:true})).toBeDisabled();
  await expect(page.getByText('启用工单通知需要 Token、至少一个接收位置和一个工单事件',{exact:true})).toBeVisible();
  await page.getByRole('checkbox',{name:'客户追加回复',exact:true}).check();
  await page.getByRole('button',{name:'保存 TG 配置',exact:true}).click();await expect.poll(()=>saves.length).toBe(1);
  const values=saves[0].items||saves[0].values||saves[0];assert.ok(Array.isArray(values),JSON.stringify(saves[0]));const settings=Object.fromEntries(values.map(v=>[v.key,JSON.parse(v.value_json)]));
  assert.equal(settings.telegram_ticket_enabled,true);assert.deepEqual(settings.telegram_ticket_events,['ticket.replied']);assert.equal(settings.telegram_order_enabled,false);assert.deepEqual(settings.telegram_targets,[{chat_id:'-10011',topic_id:7}]);assert.equal(settings.telegram_bot_token,undefined);
  await expect(page.getByRole('button',{name:'测试 -10011 · Topic 7',exact:true})).toBeEnabled();assert.equal(tests,0);
  await page.getByRole('button',{name:'测试 -10011 · Topic 7',exact:true}).click();await expect.poll(()=>tests).toBe(1);
  await expect(page.getByRole('button',{name:'测试 -10011 · Topic 7',exact:true})).toBeEnabled();
  await page.getByRole('switch',{name:'主站工单通知',exact:true}).scrollIntoViewIfNeeded();
  await page.screenshot({path:`${process.env.ZCARD_SCREEN_DIR||'/tmp'}/ticket-telegram-${width}.png`,fullPage:true});
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/ticket?ticket_no=T-FROM-TG`);await expect(page.getByText('工单 T-FROM-TG',{exact:true})).toBeVisible();await expect.poll(()=>ticketReads).toBe(1);
  assert.deepEqual(errors,[]);await page.close();console.log(`PASS ticket settings, ticket-only test, topic preservation and ticket deep link width=${width}`);
 }
}finally{await browser.close();server.close();}})().catch(e=>{console.error(e);server.close();process.exitCode=1});
