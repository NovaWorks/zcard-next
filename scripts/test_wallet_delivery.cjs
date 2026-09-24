/** Build storefront first; all APIs are local fixtures, never a real payment gateway. */
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const fs=require('node:fs'), path=require('node:path'), http=require('node:http'), assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../storefront/dist');
let balance=0, failed=false, status='pending_payment', walletReads=0, payments=0, walletDelay=0;
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://localhost');
 if(url.pathname.startsWith('/api/')){
   res.setHeader('Content-Type','application/json');let chunks=[];for await(const c of req)chunks.push(c);const body=chunks.length?JSON.parse(Buffer.concat(chunks)):{};
   let data={items:[],entries:[],total:0};
   if(url.pathname.endsWith('/config'))data={entries:[{key:'site.name',value_json:'"支付回归测试"'}]};
   if(url.pathname.endsWith('/payment/channels'))data={channels:[...(req.headers.authorization?[{code:'balance',driver:'wallet',name:'余额支付'}]:[]),{code:'epay',driver:'epay',name:'支付宝'}]};
   if(url.pathname.endsWith('/wallet')){walletReads++; if(walletDelay) await new Promise(r=>setTimeout(r,walletDelay)); if(failed){res.statusCode=503;data={message:'temporary'};}else data={available_cents:balance,locked_cents:9000,total_cents:balance+9000};}
   if(url.pathname.endsWith('/orders/wallet-test'))data={order_no:'wallet-test',status,total_cents:100,items:[{quantity:1}],expires_at:Math.floor(Date.now()/1000)+600};
   if(url.pathname.endsWith('/payment/quote'))data={base_cents:100,fee_cents:0,total_cents:100,channel:body.channel,method:body.method,quote_key:'test'};
   if(url.pathname.endsWith('/payments')){payments++; res.statusCode=400;data={message:'可用余额不足，请充值或更换支付方式'};balance=0;}
   if(url.pathname.endsWith('/delivery/fetch'))data={order_no:'wallet-test',status}; // Actual proto wire shape omits items.
   res.end(JSON.stringify(data));return;
 }
 let file=path.join(root,url.pathname);if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');
 res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch();
 try{
  for(const width of [390,1280]){
   const page=await browser.newPage({viewport:{width,height:950}});const errors=[];page.on('pageerror',e=>errors.push(e.message));
   await page.addInitScript(()=>{localStorage.setItem('zcard_token','fixture');sessionStorage.setItem('zc_pwd_wallet-test','fixture');});
   const base=`http://127.0.0.1:${server.address().port}`;status='pending_payment';failed=false;balance=0;payments=0;
   await page.goto(base+'/payment/wallet-test');const wallet=page.getByRole('button',{name:/余额支付/});
   await expect(wallet).toBeDisabled();await expect(wallet).toContainText('还差');await expect(wallet).toContainText('0.00');
   await expect(page.getByRole('button',{name:/支付宝/})).toHaveAttribute('aria-pressed','true');assert.equal(payments,0);
   balance=99;await page.getByRole('button',{name:'刷新余额',exact:true}).click();await expect(wallet).toContainText('0.01');await expect(wallet).toBeDisabled();
   balance=100;await page.getByRole('button',{name:'刷新余额',exact:true}).click();await expect(wallet).toBeEnabled();await expect(wallet).toContainText('1.00');
   await expect(page.getByRole('button',{name:/支付宝/})).toHaveAttribute('aria-pressed','true');
   await wallet.click();await page.getByRole('button',{name:/立即支付/}).click();await expect(wallet).toBeDisabled();await expect(page.locator('.pay-submit')).toBeDisabled();
   balance=200;await page.getByRole('button',{name:'刷新余额',exact:true}).click();await expect(wallet).toBeEnabled();
   failed=true;await page.getByRole('button',{name:'刷新余额',exact:true}).click();await expect(wallet).toBeDisabled();await expect(wallet).toContainText('余额查询失败');assert(!(await wallet.innerText()).includes('0.00'));
   failed=false;
   for(const st of ['pending_payment','paid','partially_delivered','canceled','expired','refunded']){
    status=st;await page.goto(base+'/fetch?order_no=wallet-test');await expect(page.locator('.result-card')).toBeVisible();await expect(page.locator('.query-hero')).toBeVisible();
    if(st==='pending_payment')await expect(page.getByRole('link',{name:'继续支付',exact:true})).toBeVisible();
    if(st==='paid')await expect(page.locator('.result-card')).toContainText('无需再次支付');
   }
   status='paid';await page.goto(base+'/payment/wallet-test');await expect(page.locator('.pay-state-title')).toHaveText('支付成功');
   assert.deepEqual(errors,[]);await page.close();console.log(`PASS balance states, concurrent debit failure, missing items, width=${width}`);
  }
  // A manual choice made while the initial wallet request is pending must survive.
  const delayed=await browser.newPage();status='pending_payment';balance=100;walletDelay=500;
  await delayed.addInitScript(()=>localStorage.setItem('zcard_token','fixture'));
  await delayed.goto(`http://127.0.0.1:${server.address().port}/payment/wallet-test`);
  await delayed.getByRole('button',{name:/支付宝/}).click();
  await expect(delayed.getByRole('button',{name:/余额支付/})).toBeEnabled();
  await expect(delayed.getByRole('button',{name:/支付宝/})).toHaveAttribute('aria-pressed','true');
  await delayed.close();walletDelay=0;console.log('PASS initial balance response preserves manual selection');
  const guest=await browser.newPage();status='pending_payment';const before=walletReads;
  await guest.goto(`http://127.0.0.1:${server.address().port}/payment/wallet-test`);await expect(guest.getByRole('button',{name:/支付宝/})).toBeEnabled();
  assert.equal(await guest.getByRole('button',{name:/余额支付/}).count(),0);assert.equal(walletReads,before);await guest.close();console.log('PASS guest has no wallet request');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
