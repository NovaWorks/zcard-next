/** Build admin first. No real Telegram messages are sent. */
const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE||'@playwright/test');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../admin/dist');
const server=http.createServer((req,res)=>{let file=path.join(root,new URL(req.url,'http://local').pathname.replace(/^\/admin\/?/,''));if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css'})[path.extname(file)]||'application/octet-stream');res.end(fs.readFileSync(file));});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch();try{
 for(const width of [390,1440]){
  const page=await browser.newPage({viewport:{width,height:1000}});const errors=[];let tests=0,retries=0,saves=0;
  page.on('pageerror',e=>errors.push(e.message));await page.addInitScript(()=>{localStorage.setItem('token',JSON.stringify('fixture'));localStorage.setItem('refreshToken',JSON.stringify('fixture'));});
  const items=[['telegram_enabled','Telegram 通知通道',true],['telegram_order_enabled','Telegram 主站订单通知',true],['telegram_bot_token','Telegram Bot Token','****'],['telegram_chat_ids','Telegram 接收 Chat ID','123'],['telegram_events','Telegram 订单通知事件',['order.paid']]].map(([key,label,value])=>({group:'notify',key,label,value_json:JSON.stringify(value),secret:key==='telegram_bot_token',options:key==='telegram_events'?[{value:'order.paid',label:'付款成功'},{value:'order.created',label:'新订单'}]:[]}));
  await page.route('**/api/v1/**',async route=>{const u=new URL(route.request().url()),p=u.pathname;let data={items:[],total:0};
   if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'test'},permissions:['*']};
   if(p.endsWith('/settings')){if(route.request().method()==='PUT'){saves++;data={updated:1};}else data={items:u.searchParams.get('group')==='notify'?items:[]};}
   if(p.endsWith('/notify/telegram/test')){tests++;data={};}
   if(p.endsWith('/notify/logs')){assert.equal(u.searchParams.get('channel'),'telegram');data={logs:[{id:1,subject:'订单付款成功',recipient:'123',status:'failed',retryable:true,attempts:2,error_message:'Telegram (403): blocked'}],total:1};}
   if(p.endsWith('/logs/1/resend')){retries++;data={};}
   await route.fulfill({json:data});
  });
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/settings`);await page.getByText('邮件短信与 Telegram',{exact:true}).click();
  await expect(page.getByRole('button',{name:'发送测试消息',exact:true})).toBeEnabled();assert.equal(tests,0);
  await page.getByRole('button',{name:'发送测试消息',exact:true}).click();await expect(page.getByText('Telegram (403): blocked',{exact:true})).toBeVisible();assert.equal(tests,1);
  await page.getByRole('button',{name:'重试此接收人',exact:true}).click();await expect.poll(()=>retries).toBe(1);
  await page.getByPlaceholder('商家个人或管理群 Chat ID，多个用英文逗号分隔；最多 20 个').fill('456');
  await expect(page.getByRole('button',{name:'发送测试消息',exact:true})).toBeDisabled();
  await page.getByRole('button',{name:'保存更改',exact:true}).click();await expect.poll(()=>saves).toBe(1);await expect(page.getByRole('button',{name:'发送测试消息',exact:true})).toBeEnabled();
  assert.deepEqual(errors,[]);await page.screenshot({path:`/tmp/zcard-telegram-${width}.png`,fullPage:true});await page.close();console.log(`PASS Telegram configuration, explicit test, logs, retry width=${width}`);
 }
}finally{await browser.close();server.close();}})().catch(e=>{console.error(e);server.close();process.exitCode=1});
