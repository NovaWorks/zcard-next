/** Production admin dashboard fixtures; no live orders, payments or credentials. */
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE||'@playwright/test');
const root=path.resolve(__dirname,'../admin/dist');
const server=http.createServer((req,res)=>{let file=path.join(root,new URL(req.url,'http://localhost').pathname.replace(/^\/admin\/?/,''));if(!fs.existsSync(file)||fs.statSync(file).isDirectory())file=path.join(root,'index.html');res.setHeader('Content-Type',({'.html':'text/html','.js':'application/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true});
 try{for(const width of [1440,390]){
 const page=await browser.newPage({viewport:{width,height:1000}});let mode='normal';const requests=[],errors=[];
 page.on('pageerror',e=>errors.push(e.message));await page.addInitScript(()=>{localStorage.setItem('token',JSON.stringify('fixture'));localStorage.setItem('refreshToken',JSON.stringify('fixture'));});
 await page.route('**/api/v1/**',async route=>{
 const u=new URL(route.request().url()),p=u.pathname;requests.push(u);let data={items:[],orders:[],payments:[],refunds:[],channels:[],drivers:[],users:[],total:0,points:[]};
 if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'fixture'},permissions:['*']};
 if(p.endsWith('/dashboard')){
 if(mode==='error')return route.fulfill({status:500,json:{code:500,reason:'test.ERROR',message:'统计暂不可用'}});
 const days=Number(u.searchParams.get('trend_days')||1),zero=mode==='empty';
 const stat=zero?{}:{orders:8,paid_orders:5,revenue:30000,refunds:4000,net_revenue:26000,cost:10000,profit:16000,unknown_cost_orders:1,new_users:2};
 data={today:stat,last7d:stat,last30d:stat,yesterday:{},prev7d:{},prev30d:{},generated_at:1790035200,range_start:1790035200-days*86400,range_end:1790035200,subsite_id:0,
 trend:[{date:'2026-09-22',revenue:zero?0:30000,paid_count:zero?0:5,orders:zero?0:8}],pending:{payment_reviews:zero?0:2,fulfilling_orders:1,pending_refunds:1},
 top_products:zero?[]:[{product_id:7,name:'回归商品',revenue:30000,sold_qty:5}],top_channels:zero?[]:Array.from({length:7},(_,i)=>({channel_id:i+1,channel:'be-'+i,name:'渠道'+(i+1),channel_state:i===0?'deleted':i===1?'disabled':'active',amount:1000,total_count:1}))};
 }
 if(p.endsWith('/payments'))data={payments:[{id:1,order_no:'已删除订单的历史到账',status:'success',review_reason:'迟到到账待核对',amount_cents:100}],next_cursor:0};
 await route.fulfill({json:data});
 });
 const home=`http://127.0.0.1:${server.address().port}/admin/home`;await page.goto(home);
 await expect(page.locator('main')).toContainText('1 单成本不完整');await expect(page.locator('main')).toContainText('渠道1（已删除）');await expect(page.locator('main')).toContainText('渠道2（已停用）');
 await expect(page.locator('main')).toContainText('14.3%');await expect(page.locator('main')).toContainText('28.6%');assert(!(await page.locator('main').innerText()).includes('NaN'));
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth+1),false,'viewport overflow');
 await page.getByText('近7天',{exact:true}).click();await expect.poll(()=>requests.filter(u=>u.pathname.endsWith('/dashboard')).at(-1).searchParams.get('trend_days')).toBe('7');
 await expect(page.locator('main')).toContainText('订单支付方式分布 · 近7天');
 await page.getByRole('button',{name:'查看支付订单数明细'}).click();await expect.poll(()=>requests.filter(u=>u.pathname.endsWith('/orders')).at(-1)?.searchParams.get('time_field')).toBe('paid');
 await expect(page.locator('main')).toContainText('工作台筛选');assert.equal(requests.filter(u=>u.pathname.endsWith('/orders')).at(-1).searchParams.get('start_time'),String(1790035200-7*86400));
 await page.goBack();await expect(page.locator('main')).toContainText('到账待核对');
 await page.getByText('到账待核对',{exact:true}).click();await expect.poll(()=>requests.filter(u=>u.pathname.endsWith('/payments')).at(-1)?.searchParams.get('review_only')).toBe('true');await expect(page.locator('main')).toContainText('已删除订单的历史到账');
 mode='empty';await page.goto(home);await expect(page.locator('main')).toContainText('该时段暂无支付订单');await expect(page.locator('main')).toContainText('该时段暂无订单支付');
 mode='error';await page.reload();await expect(page.locator('main')).toContainText('统计加载失败，请重试');
 mode='normal';await page.getByRole('button',{name:'重新加载',exact:true}).click();await expect(page.locator('main')).toContainText('1 单成本不完整');await expect(page.locator('main')).not.toContainText('统计加载失败，请重试');
 await expect(page.locator("main canvas").first()).toBeAttached();
 await page.waitForTimeout(1200); // Let chart transition and transient error toast finish before visual review.
 await page.screenshot({path:`/tmp/zcard-dashboard-${width}.png`,fullPage:true});assert.deepEqual(errors,[]);await page.close();console.log(`PASS dashboard ${width}: zero fields, unknown cost, deleted channels, full denominator, unified window, order/review drilldown, empty state and error retry`);
 }}finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
