/** Build admin first. Browser checks use only mocked orders and supplier receipts. */
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http'), assert = require('node:assert/strict');
const root = path.resolve(__dirname, '../admin/dist');
const server = http.createServer((req,res) => { let file = path.join(root,new URL(req.url,'http://local').pathname.replace(/^\/admin\/?/,'')); if (!fs.existsSync(file)||fs.statSync(file).isDirectory()) file=path.join(root,'index.html'); res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css'})[path.extname(file)]||'application/octet-stream'); res.end(fs.readFileSync(file)); });
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch();
 try{for(const width of [390,1440]){
  const page=await browser.newPage({viewport:{width,height:1100}});const errors=[],lookups=[],manualRequests=[];let retries=0,transfers=0,deliveryError=true;
  const item={id:207,product_id:10,name:'测试上游商品',quantity:1,unit_price:24,fulfillment_type:'upstream',fulfillment_status:'pending'};
  const order={id:100,order_no:'S-TEST-207',status:'fulfilling',total_cents:24,items:[item],events:[]};
  const po={id:121,order_item_id:207,order_no:order.order_no,order_status:order.status,status:'fulfilled',received_cards:1,item_quantity:1,delivery_incomplete:true,product_name:item.name};
  const procurements=[po];
  page.on('pageerror',e=>errors.push(e.message));await page.addInitScript(()=>{localStorage.setItem('token',JSON.stringify('fixture'));localStorage.setItem('refreshToken',JSON.stringify('fixture'));});
  await page.route('**/api/v1/**',async route=>{
   const req=route.request(),u=new URL(req.url()),p=u.pathname;let data={items:[],total:0};
   if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'test'},permissions:['*']};
   if(p.endsWith('/orders'))data={orders:[order],total:1};
   if(p.endsWith('/orders/S-TEST-207'))data=order;
   if(p.endsWith('/procurements')){const no=u.searchParams.get('order_no');lookups.push(no);data={procurements:no&&no!==order.order_no?[]:procurements,total:no&&no!==order.order_no?0:procurements.length};}
   if(p.endsWith('/procurements/121'))data=po;
   if(p.endsWith('/procurements/121/retry')){retries++;po.status='fulfilled';po.delivery_incomplete=false;data=po;}
   if(p.endsWith('/procurements/121/manual')){transfers++;po.status='manual';data=po;}
   if(p.endsWith('/deliver')){
    manualRequests.push(req.postDataJSON());
    if(deliveryError){await route.fulfill({status:400,json:{code:400,reason:'fulfillment.DELIVER_FAILED',message:'该商品待补发 1 条，请每行填写一条卡密'}});return;}
    item.fulfillment_status='delivered';order.status='delivered';po.status='fulfilled';po.delivery_incomplete=false;data={};
   }
   await route.fulfill({json:data});
  });
  const base=`http://127.0.0.1:${server.address().port}/admin`;
  await page.goto(`${base}/channel?tab=procurement&order_no=S-TEST-207`);
  await expect(page.getByPlaceholder('输入完整客户订单号')).toHaveValue(order.order_no);
  await expect(page.locator('main')).toContainText(order.order_no);
  await expect(page.getByText('交付异常',{exact:true})).toBeVisible();
  await page.getByRole('button',{name:'重试交付',exact:true}).click();await expect.poll(()=>retries).toBe(1);
  await page.getByPlaceholder('输入完整客户订单号').fill('MISSING');await page.getByRole('button',{name:'查询',exact:true}).click();
  await expect(page.getByText(/未找到该客户订单的采购单/)).toBeVisible();
  po.delivery_incomplete=true;
  await page.goto(`${base}/order?order_no=S-TEST-207`);
  await page.getByRole('button',{name:'人工补发',exact:true}).click();
  const dialog=page.locator('.n-modal').filter({has:page.getByRole('button',{name:'确认补发',exact:true})});
  await expect(dialog).toContainText('关联采购单 #121');await expect(dialog.getByRole('button',{name:'确认补发',exact:true})).toBeDisabled();
  await expect(dialog.getByRole('button',{name:'重试已有卡密交付',exact:true})).toBeVisible();
  await dialog.getByRole('button',{name:'核实后转人工',exact:true}).click();
  await page.getByRole('button',{name:'确认',exact:true}).click();await expect.poll(()=>transfers).toBe(1);
  await expect(dialog.getByRole('button',{name:'确认补发',exact:true})).toBeEnabled();
  await dialog.getByPlaceholder('粘贴需要补发的卡密').fill('TEST-CARD');await dialog.getByRole('button',{name:'确认补发',exact:true}).click();
  await expect(dialog).toContainText('该商品待补发 1 条，请每行填写一条卡密');
  assert.equal(manualRequests.at(-1).order_item_id,207);
  await page.screenshot({path:`/tmp/zcard-procurement-recovery-${width}.png`,fullPage:true});
  deliveryError=false;await dialog.getByRole('button',{name:'确认补发',exact:true}).click();await expect(dialog).toHaveCount(0);
  await page.getByRole('button',{name:'关联采购单',exact:true}).click();await expect(page.getByPlaceholder('输入完整客户订单号')).toHaveValue(order.order_no);
  // Multi-item: selection must control the procurement guard and submitted item ID.
  order.status='fulfilling';item.fulfillment_status='pending';po.status='submitted';po.delivery_incomplete=true;
  order.items.push({...item,id:208,name:'第二个上游商品'});
  procurements.push({...po,id:122,order_item_id:208,status:'manual'});
  await page.goto(`${base}/order?order_no=S-TEST-207`);await page.getByRole('button',{name:'人工补发',exact:true}).click();
  await expect(dialog.getByRole('button',{name:'确认补发',exact:true})).toBeDisabled();
  await dialog.locator('.n-select').click();await page.getByText('第二个上游商品 · 购买 1 件',{exact:true}).click();
  await expect(dialog.getByRole('button',{name:'确认补发',exact:true})).toBeEnabled();
  await dialog.getByPlaceholder('粘贴需要补发的卡密').fill('SECOND');await dialog.getByRole('button',{name:'确认补发',exact:true}).click();await expect(dialog).toHaveCount(0);
  assert.equal(manualRequests.at(-1).order_item_id,208);assert.equal(transfers,1);
  // A genuinely missing procurement must not trap the operator.
  procurements.splice(0);order.status='fulfilling';order.items.splice(0,order.items.length,{...item,id:209,name:'缺失采购商品',fulfillment_status:'pending'});
  await page.goto(`${base}/order?order_no=S-TEST-207`);await page.getByRole('button',{name:'人工补发',exact:true}).click();
  await expect(dialog).toContainText('未找到该商品的采购单');await expect(dialog.getByRole('button',{name:'确认补发',exact:true})).toBeEnabled();
  await dialog.getByPlaceholder('粘贴需要补发的卡密').fill('MISSING');await dialog.getByRole('button',{name:'确认补发',exact:true}).click();await expect(dialog).toHaveCount(0);assert.equal(manualRequests.at(-1).order_item_id,209);
  assert(lookups.includes(order.order_no)&&lookups.includes('MISSING'));assert.deepEqual(errors,[]);
  await page.close();console.log(`PASS procurement lookup, old receipt retry, inline manual transfer, real error display, order link width=${width}`);
 }}finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
