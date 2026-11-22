/** Build admin first. All supplier responses are fixtures; no purchases occur. */
const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE||'@playwright/test');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const root=path.resolve(process.env.ZCARD_ADMIN_DIST||path.join(__dirname,'../admin/dist'));
const server=http.createServer((req,res)=>{let f=path.join(root,new URL(req.url,'http://local').pathname.replace(/^\/admin\/?/,''));if(!fs.existsSync(f)||fs.statSync(f).isDirectory())f=path.join(root,'index.html');res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css'})[path.extname(f)]||'application/octet-stream');res.end(fs.readFileSync(f));});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch();try{for(const width of [390,1440]){
 const context=await browser.newContext({viewport:{width,height:1050},permissions:['clipboard-read','clipboard-write']});const page=await context.newPage();const errors=[],mutations=[];
 const source={kind:'upstream',stage:'purchase',connection_id:'9',connection_name:'原萌次元渠道',product_code:'12~52',product_id:'12',sku_id:'52',website_url:'https://original.example',product_url:'https://original.example/item?id=12',link_status:'product'};
 const second={...source,connection_id:'10',connection_name:'第二渠道',product_id:'88',sku_id:'99',product_url:'https://second.example/item?id=88'};
 const item={id:207,product_id:10,name:'第一商品',quantity:1,fulfillment_type:'upstream',fulfillment_status:'pending',upstream_source:source};
 const order={id:100,order_no:'S-SOURCE',status:'fulfilling',total_cents:100,items:[item,{...item,id:208,name:'第二商品',upstream_source:second}],events:[]};
 const po={id:121,order_item_id:207,order_no:order.order_no,order_status:order.status,status:'manual',upstream_order_id:'UP-ORIGINAL',dedupe_key:'ORIGINAL-REQUEST',received_cards:0,item_quantity:1,product_name:item.name,upstream_source:source};
 page.on('pageerror',e=>errors.push(e.message));await page.addInitScript(()=>{localStorage.setItem('token',JSON.stringify('fixture'));localStorage.setItem('refreshToken',JSON.stringify('fixture'));});
 await page.route('**/api/v1/**',async route=>{const req=route.request(),p=new URL(req.url()).pathname;if(req.method()!=='GET')mutations.push(p);let data={items:[],total:0};
 if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'test'},permissions:['*']};
 if(p.endsWith('/orders'))data={orders:[order],total:1};if(p.endsWith('/orders/S-SOURCE'))data=order;
 if(p.endsWith('/procurements'))data={procurements:[po],total:1};if(p.endsWith('/procurements/121'))data=po;
 if(p.endsWith('/products'))data={products:[{id:10,name:'当前商品',upstream_source_id:9,upstream_source:{...source,stage:'current'},price_cents:100,status:1}],total:1};
 if(p.endsWith('/supply-procurements'))data={items:[{id:'7',account_id:'3',downstream_order_no:'DOWN-SOURCE',request_no:'market-card-7',upstream_order_id:'UP-MARKET',status:'fulfilling',product_name:'Market商品',upstream_source:source}],total:1};
 await route.fulfill({json:data});});
 const base=`http://127.0.0.1:${server.address().port}/admin`;
 await page.goto(`${base}/channel?tab=procurement`);let link=page.getByRole('link',{name:'打开上游商品 ↗'});await expect(link).toHaveAttribute('href',source.product_url);await expect(link).toBeInViewport();await expect(link).toHaveAttribute('rel','noopener noreferrer');
 await page.getByRole('button',{name:'查看',exact:true}).click();const modal=page.locator('.n-modal');await expect(modal).toContainText('实际采购来源');await expect(modal).toContainText('UP-ORIGINAL');await expect(modal).toContainText('ORIGINAL-REQUEST');
 await modal.getByRole('button',{name:'复制商品编号',exact:true}).click();assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),'12');await modal.getByRole('button',{name:'复制规格编号',exact:true}).click();assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),'52');
 await page.screenshot({path:`/tmp/zcard-upstream-source-procurement-${width}.png`,fullPage:true});
 await page.goto(`${base}/order?order_no=S-SOURCE`);await expect(page.getByText('各商品拿货来源',{exact:true})).toBeVisible();link=page.getByRole('link',{name:'打开上游商品 ↗'});await expect(link).toHaveCount(2);await expect(link.nth(0)).toHaveAttribute('href',source.product_url);await expect(link.nth(1)).toHaveAttribute('href',second.product_url);
 await page.goto(`${base}/product`);await expect(page.getByRole('link',{name:'打开上游商品 ↗'})).toHaveAttribute('href',source.product_url);
 if(process.env.ZCARD_MARKET_SOURCE){await page.goto(`${base}/channel?tab=supply-procurement`);await expect(page.getByRole('link',{name:'打开上游商品 ↗'})).toHaveAttribute('href',source.product_url);await page.getByRole('button',{name:'查看采购定位'}).click();await expect(page.locator('.n-modal')).toContainText('market-card-7');}
 assert.deepEqual(mutations,[]);assert.deepEqual(errors,[]);await context.close();console.log(`PASS upstream source links, independent items, copy IDs, no mutations width=${width}`);
 }}finally{await browser.close();server.close();}})().catch(e=>{console.error(e);server.close();process.exitCode=1});
