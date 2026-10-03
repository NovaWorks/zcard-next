/** Real HTTP + production storefront regression against a disposable local instance.
 * PHYSICAL_TEST_DIR must contain the isolated configs/config.yaml and admin-auth.json.
 * No remote payment gateway or email service is used. */
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http'), net = require('node:net');
const { createHash, randomUUID } = require('node:crypto');
const instance = process.env.PHYSICAL_TEST_DIR;
assert(instance && path.basename(instance).startsWith('zcard-physical-e2e-'), 'Use a disposable physical test instance');
const config = JSON.parse(fs.readFileSync(path.join(instance, 'configs/config.yaml')));
assert(config.data.database.source.startsWith('file:' + instance + '/'), 'Test database must stay inside the disposable instance');
assert(config.server.http.addr.startsWith('127.0.0.1:'), 'Only loopback test APIs are allowed');
const backend = 'http://' + config.server.http.addr;
const adminToken = JSON.parse(fs.readFileSync(path.join(instance, 'admin-auth.json'))).access_token;
const output = path.join(instance, 'screenshots'); fs.mkdirSync(output, { recursive: true });
const root = path.resolve(__dirname, '../storefront/dist');
let emails = [], paymentBody, paymentReply, orderBody, createdOrder;
async function request(route, body, admin = false, method = 'POST') {
  const r = await fetch(backend + route, { method, headers: { 'Content-Type': 'application/json', ...(admin ? {Authorization: 'Bearer ' + adminToken} : {}) }, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await r.text();
  assert(r.ok, route + ': ' + text);
  return text ? JSON.parse(text) : {};
}
const smtp = net.createServer(socket => {
  socket.write('220 local.test ESMTP\r\n'); let buffer = '', data = false, message = '';
  socket.on('data', chunk => {
    buffer += chunk;
    while (buffer.includes('\r\n')) {
      const at = buffer.indexOf('\r\n'), line = buffer.slice(0, at); buffer = buffer.slice(at + 2);
      if (data) {
        if (line === '.') { emails.push(message); data = false; message = ''; socket.write('250 accepted\r\n'); }
        else message += line + '\n';
      } else if (/^(EHLO|HELO)/i.test(line)) socket.write('250 local.test\r\n');
      else if (/^DATA/i.test(line)) { data = true; socket.write('354 send message\r\n'); }
      else if (/^QUIT/i.test(line)) { socket.end('221 bye\r\n'); }
      else socket.write('250 ok\r\n');
    }
  });
});
const frontend = http.createServer(async (req, res) => {
  const u = new URL(req.url, 'http://localhost');
  if (u.pathname.startsWith('/api/') || u.pathname.startsWith('/payments/callback/')) {
    const chunks = []; for await (const chunk of req) chunks.push(chunk);
    const raw = Buffer.concat(chunks);
    const r = await fetch(backend + req.url, {method: req.method, headers: {'Content-Type': req.headers['content-type'] || 'application/json', 'Accept-Language': req.headers['accept-language'] || 'zh-CN', ...(req.headers.authorization ? {Authorization:req.headers.authorization} : {}), ...(req.headers['idempotency-key'] ? {'Idempotency-Key':req.headers['idempotency-key']} : {}), ...(req.headers['x-order-access-token'] ? {'X-Order-Access-Token':req.headers['x-order-access-token']} : {})}, body: ['GET','HEAD'].includes(req.method) ? undefined : raw});
    const text = await r.text();
    if (r.ok && req.method === 'POST' && u.pathname === '/api/v1/storefront/orders') { orderBody = JSON.parse(raw); createdOrder = JSON.parse(text); }
    if (r.ok && u.pathname === '/api/v1/storefront/payments') { paymentBody = JSON.parse(raw); paymentReply = JSON.parse(text); }
    res.statusCode = r.status; res.setHeader('Content-Type', r.headers.get('content-type') || 'application/json'); res.end(text); return;
  }
  if (u.pathname === '/local-gateway') { res.end('Local gateway fixture'); return; }
  const staticRoot=u.pathname.startsWith('/admin/')?path.resolve(__dirname,'../admin/dist'):root;
  let file = path.join(staticRoot, staticRoot===root?u.pathname:u.pathname.slice('/admin'.length));
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(staticRoot, 'index.html');
  res.setHeader('Content-Type', ({'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)] || 'application/octet-stream'); res.end(fs.readFileSync(file));
});
async function waitFor(predicate, label) {
  for (let i=0; i<100; i++) { if(await predicate()) return; await new Promise(r=>setTimeout(r,100)); }
  throw Error('Timed out: ' + label);
}
(async () => {
  await new Promise(r => smtp.listen(0, '127.0.0.1', r)); await new Promise(r => frontend.listen(0, '127.0.0.1', r));
  await waitFor(async()=>{try{return (await fetch(backend+'/health')).ok}catch{return false}},'test API startup');
  const origin = 'http://127.0.0.1:' + frontend.address().port;
  const values = {'site.url':origin, 'template.show_stock':true, 'template.show_sales':true, 'trade.query_password':true, 'security.captcha_order':false, 'notify.smtp_host':'127.0.0.1', 'notify.smtp_port':smtp.address().port, 'notify.smtp_from':'sender@example.test', 'notify.smtp_security':'plain', 'notify.smtp_auth':'none', 'i18n.enabled_locales':['zh_CN','en'], 'i18n.default_locale':'zh_CN'};
  await request('/api/v1/admin/settings', {items:Object.entries(values).map(([full,value])=>{const [group,key]=full.split('.');return {group,key,value_json:JSON.stringify(value)}})}, true, 'PUT');
  const product = await request('/api/v1/admin/products', {name:'实体流程浏览器验收'+Date.now(),stock_type:'card',price_cents:1000,status:1,product_property:'physical',track_inventory:false,stock_visible:false,sales_visible:false,shipping_mode:'free',shipping_countries:['AU','GB','HK']}, true);
  assert.equal(product.stock_visible, false, 'false display switch must be present in JSON');
  const channel = await request('/api/v1/admin/payment/channels', {name:'本地验收支付'+Date.now(),code:'physical-' + Date.now(),driver:'epay',config_json:JSON.stringify({api_url:origin+'/local-gateway',pid:'1',key:'physical-fixture-only'}),enabled:true,allow_purchase:true}, true);
  const browser = await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
  try {
    const context = await browser.newContext({viewport:{width:390,height:900}}); const page = await context.newPage(); const errors=[]; page.on('pageerror',e=>errors.push(e.message)); page.on('dialog',d=>d.accept());
    await page.goto(origin + '/product/' + product.id); await page.locator('.pd-name').waitFor();
    assert.equal(await page.locator('.pd-stats').count(),0); assert.equal(await page.locator('.pd-field').filter({hasText:'查询密码'}).count(),0);
    await page.locator('.pd-field input[type=email]').fill('buyer@example.test'); await page.locator('.pd-btn-buy').click(); await page.locator('#shipping-name').fill('Test Buyer'); await page.locator('#shipping-phone').fill('+61412345678');
    await page.locator('#shipping-country').selectOption('AU'); await page.locator('#shipping-address').fill('3 Example Street'); await page.locator('#shipping-address_line2').fill('Unit 8'); await page.locator('#shipping-city').fill('Sydney'); await page.locator('#shipping-region').selectOption('NSW'); await page.locator('#shipping-postal_code').fill('2000');
    await page.locator('.shipping-submit').click(); await page.getByText('确认地址及金额，创建订单',{exact:true}).waitFor(); await page.screenshot({path:path.join(output,'checkout-mobile.png'),fullPage:true,animations:'disabled'}); await page.locator('.shipping-submit').click(); await page.locator('.pay-submit').waitFor();
    assert.equal(orderBody.query_password,undefined); assert.equal(orderBody.shipping_address.address_line2,'Unit 8'); assert.match(createdOrder.order_access_token,/^[A-Za-z0-9_-]{43}$/);
    const no = createdOrder.order_no, initialToken = createdOrder.order_access_token;
    await page.locator('.pay-channel').filter({hasText:channel.name}).click(); await page.locator('.pay-submit').click(); await waitFor(()=>paymentReply,'payment creation'); assert.equal(paymentBody.order_access_token,initialToken);
    const params=JSON.parse(paymentReply.payload).params;
    const callback={pid:'1',trade_no:'LOCAL-'+Date.now(),out_trade_no:params.out_trade_no,money:params.money,trade_status:'TRADE_SUCCESS',sign_type:'MD5'};
    callback.sign=createHash('md5').update(Object.keys(callback).filter(k=>k!=='sign_type').sort().map(k=>k+'='+callback[k]).join('&')+'physical-fixture-only').digest('hex');
    const callbackResponse=await fetch(backend+'/payments/callback/'+channel.code+'?channel_id='+channel.id,{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams(callback)}); assert.equal(await callbackResponse.text(),'success');
    await page.getByText('支付成功',{exact:true}).waitFor({timeout:15000}); await page.goto(origin+'/order/'+no); await page.locator('.shipping-details').waitFor(); assert((await page.locator('.shipping-details').innerText()).includes('Unit 8'));
    const detail=await request('/api/v1/admin/orders/'+no,undefined,true,'GET'); assert.equal(detail.items[0].inventory_tracked,false);
    await request('/api/v1/admin/fulfillment/'+no+'/ship',{item_ids:[detail.items[0].id],carrier:'Local Post',tracking_no:'LOCAL001',request_key:randomUUID()},true);
    await page.reload(); await page.getByText('确认收到此包裹',{exact:true}).waitFor(); await page.getByText('确认收到此包裹',{exact:true}).click();
    await page.locator('.shipment b').filter({hasText:'已收货'}).waitFor(); await page.screenshot({path:path.join(output,'received-mobile.png'),fullPage:true,animations:'disabled'});
    const lookupRequests=[]; page.on('request',r=>lookupRequests.push(r.url())); await page.goto(origin+'/fetch?order_no='+no); await page.locator('.shipping-details').waitFor(); assert(!lookupRequests.some(u=>u.includes('/delivery/') || u.includes('/deliveries/')),'physical order must not fetch card delivery');
    await context.close();
    const fresh=await browser.newContext({viewport:{width:1280,height:900}}), recovery=await fresh.newPage(); await recovery.goto(origin+'/order/'+no); await recovery.locator('.order-recovery').waitFor(); await recovery.locator('.order-recovery input[type=email]').fill('buyer@example.test'); const before=emails.length; await recovery.getByRole('button',{name:'发送验证码',exact:true}).click(); await waitFor(()=>emails.length>before,'local recovery email'); const code=emails.at(-1).match(/<b>(\d{6})<\/b>/)[1]; await recovery.locator('input[autocomplete="one-time-code"]').fill(code); await recovery.getByRole('button',{name:'验证并查看订单',exact:true}).click(); await recovery.locator('.shipping-details').waitFor();
    const rotated=await recovery.evaluate(no=>sessionStorage.getItem('zc_access_'+no),no); assert(rotated && rotated!==initialToken); await recovery.screenshot({path:path.join(output,'recovered-desktop.png'),fullPage:true,animations:'disabled'});
    const invalid=await fetch(backend+'/api/v1/storefront/orders/'+no,{headers:{'X-Order-Access-Token':initialToken}}); assert.equal(invalid.status,404); assert.equal(invalid.headers.get('cache-control'),'no-store, private'); assert.deepEqual(errors,[]); await fresh.close();
    const linked=await browser.newContext(), linkPage=await linked.newPage(); await linkPage.goto(origin+'/fetch?order_no='+no+'#order_access='+rotated); await linkPage.locator('.shipping-details').waitFor(); assert(!linkPage.url().includes('order_access=')); await linked.close();
    const management=await browser.newContext({viewport:{width:1440,height:1000}}); await management.addInitScript(token=>localStorage.setItem('token',JSON.stringify(token)),adminToken); const admin=await management.newPage(); await admin.goto(origin+'/admin/product'); const row=admin.getByRole('row').filter({hasText:product.name}); await row.waitFor(); assert((await row.innerText()).includes('实体')); await row.getByRole('button',{name:'编辑',exact:true}).click(); const inventory=admin.locator('.n-form-item').filter({has:admin.getByText('库存管理',{exact:true})}); await inventory.waitFor(); assert.equal(await inventory.getByRole('switch').getAttribute('aria-checked'),'false'); assert.equal(await admin.getByText('可售库存',{exact:true}).count(),0); assert.equal(await admin.getByText('交付方式',{exact:true}).count(),0); assert.equal(await admin.getByText('高级设置',{exact:true}).count(),0); await admin.screenshot({path:path.join(output,'admin-physical-settings.png'),fullPage:true,animations:'disabled'}); await admin.getByText('价格库存',{exact:true}).click(); for(const label of ['前台显示库存','前台显示销量']) { const setting=admin.locator('.n-form-item').filter({has:admin.getByText(label,{exact:true})}); const toggle=setting.getByRole('switch'); assert.equal(await toggle.getAttribute('aria-checked'),'false'); await toggle.click(); } await admin.getByText('规格与控件',{exact:true}).click(); await admin.getByRole('button',{name:'保存',exact:true}).click(); await row.waitFor(); const saved=await request('/api/v1/admin/products/'+product.id,undefined,true,'GET'); assert.equal(saved.stock_visible,true); assert.equal(saved.sales_visible,true); assert.equal(saved.track_inventory,false); await management.close();
    const display=await browser.newPage(); await display.goto(origin+'/product/'+product.id); await display.locator('.pd-stats').waitFor(); assert.equal(await display.locator('.pd-stat').count(),1); assert((await display.locator('.pd-stat').innerText()).includes('销量')); await display.goto(origin+'/products'); const card=display.locator('.product-card').filter({hasText:product.name}); await card.waitFor(); assert.equal(await card.locator('.pc-sales').count(),1); assert.equal(await card.locator('.pc-stock-free').count(),0); await display.close();
    console.log('Real HTTP + browser: physical property, false display flags, password-free checkout, two-line address, payment authorization/callback, shipping/receive, physical lookup, email recovery, token rotation, private caching and admin switches passed.'); console.log('Screenshots: '+output);
  } finally { await browser.close(); }
})().catch(e=>{console.error(e);process.exitCode=1}).finally(()=>{frontend.close();smtp.close();});
