const {chromium}=require(process.env.ZCARD_PLAYWRIGHT_MODULE || 'playwright');
(async()=>{
const b=await chromium.launch({headless:true,channel:'chrome'});
try{
const c=await b.newContext({viewport:{width:390,height:844},permissions:['clipboard-read','clipboard-write']});
await c.addInitScript(()=>{localStorage.setItem('zcard_token','fixture-only');Object.defineProperty(crypto,'randomUUID',{value:undefined});});
let snap={state:'waiting_sms',phase:'query',phone_number:'+001-23456789',expires_at:new Date(Date.now()+120000).toISOString(),can_cancel:true,can_finish:false,active:true,refund_status:'none'};let actionIDs=[],finishIDs=[];let actionMode='reject';
await c.route('**/api/v1/**',async r=>{const u=new URL(r.request().url());let data={};
if(u.pathname.endsWith('/config'))data={entries:[{key:'site.name',value_json:'"接码验收"'}]};
else if(u.pathname.endsWith('/orders/SMS-TEST'))data={order_no:'SMS-TEST',status:'paid',commerce_version:0,total_cents:999,created_at:1790700000,items:[{product_name:'接码测试',delivery_kind:'sms_activation',sms_product:{platform_id:'platform-A',country_id:'country-B'},unit_price_cents:999,amount_cents:999,quantity:1,fulfillment_type:'upstream',fulfillment_status:'pending',goods_type:'virtual'}]};
else if(u.pathname.endsWith('/sms'))data=snap;
else if(u.pathname.endsWith('/sms/cancel')){actionIDs.push(JSON.parse(r.request().postData()).request_id);if(actionMode==='reject'){snap={...snap,can_cancel:false,can_finish:true};return r.fulfill({status:400,contentType:'application/json',body:JSON.stringify({message:'当前不允许取消'})});}if(actionMode==='lost'){actionMode='accept';return r.abort('failed');}snap={...snap,can_cancel:false,operation_status:'pending',operation_action:'cancel'};data=snap;}
else if(u.pathname.endsWith('/sms/finish')){finishIDs.push(JSON.parse(r.request().postData()).request_id);snap={...snap,can_finish:false,operation_status:'succeeded',operation_action:'finish'};data=snap;}
else if(u.pathname.endsWith('/currencies'))data={currencies:[]};
else if(u.pathname.endsWith('/cart'))data={items:[]};
else if(u.pathname.endsWith('/me'))data={id:'1',username:'fixture'};
await r.fulfill({status:200,contentType:'application/json',body:JSON.stringify(data)});
});
const page=await c.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept());
await page.goto((process.env.ZCARD_SMS_PREVIEW_URL || 'http://127.0.0.1:19528') + '/order/SMS-TEST');await page.getByRole('button',{name:'复制号码',exact:true}).waitFor({timeout:8000}).catch(async e=>{console.log(await page.locator('body').innerText(),errors);await page.screenshot({path:'/tmp/zcard-sms-browser-debug.png',fullPage:true});throw e});
await page.getByRole('button',{name:'复制号码',exact:true}).click();if(await page.evaluate(()=>navigator.clipboard.readText())!==snap.phone_number)throw Error('phone clipboard mismatch');
await page.getByRole('button',{name:'申请取消',exact:true}).click();
await page.getByText('当前不允许取消',{exact:true}).waitFor();
await page.getByRole('button',{name:'完成接码',exact:true}).click();
await page.waitForFunction(()=>!document.querySelector('.sms-order')?.textContent?.includes('当前不允许取消'));
if(finishIDs.length!==1)throw Error('definitive failure blocked another action');
snap={...snap,can_cancel:true,can_finish:false,operation_status:'',operation_action:''};actionMode='lost';
await page.getByRole('button',{name:'刷新状态',exact:true}).click();
await page.getByRole('button',{name:'申请取消',exact:true}).click();
await page.getByRole('button',{name:'重试确认取消结果',exact:true}).click();
await page.getByText('取消请求已提交，等待确认。',{exact:true}).waitFor();
if(actionIDs[1]!==actionIDs[2])throw Error('uncertain action changed id');
if(actionIDs.length!==3||!actionIDs[0])throw Error('operation identity missing');
snap={...snap,state:'sms_received',otp_code:'',otp_message:'正文没有数字验证码\n<script>window.SMS_EXECUTED=true</script>',operation_status:'rejected',can_cancel:false,can_finish:true};
await page.getByRole('button',{name:'刷新状态',exact:true}).click();await page.getByText('短信接码 · 已收到短信',{exact:true}).waitFor();
if(await page.evaluate(()=>!!window.SMS_EXECUTED))throw Error('HTML executed');
if(await page.getByRole('button',{name:'复制验证码',exact:true}).count())throw Error('invented OTP');
await page.getByRole('button',{name:'复制正文',exact:true}).click();if(await page.evaluate(()=>navigator.clipboard.readText())!==snap.otp_message)throw Error('body clipboard mismatch');
if(await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth))throw Error('mobile overflow');
await page.screenshot({path:'/tmp/zcard-sms-mobile.png',fullPage:true});
snap={...snap,state:'completed',phase:'done',can_finish:false,active:false};await page.getByRole('button',{name:'刷新状态',exact:true}).click();await page.getByText('短信接码 · 接码已结束',{exact:true}).waitFor();
if(await page.getByRole('button',{name:'完成接码',exact:true}).count())throw Error('terminal action visible');
await page.setViewportSize({width:1440,height:1000});await page.screenshot({path:'/tmp/zcard-sms-desktop.png',fullPage:true});
if(errors.length)throw Error(errors.join('\n'));
console.log('PASS 400 recovery, lost-response same-ID retry, HTTP UUID fallback, mobile+desktop: phone/body copy, body-only SMS, escaped markup, cancel pending/rejected, terminal controls, no overflow/runtime errors');
}finally{await b.close();}
})().catch(e=>{console.error(e);process.exit(1)});
