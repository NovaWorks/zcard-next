// Local UI contract fixtures: durable progress, navigation, recovery and stock-only retry.
const fs = require('node:fs'), path = require('node:path'), http = require('node:http'), assert = require('node:assert/strict');
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const root = path.resolve(__dirname, '../admin/dist');
const server = http.createServer((req, res) => {
  let file = path.join(root, new URL(req.url, 'http://localhost').pathname.replace(/^\/admin\/?/, ''));
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
  res.setHeader('Content-Type', ({'.html':'text/html','.js':'application/javascript','.css':'text/css'})[path.extname(file)] || 'application/octet-stream');
  res.end(fs.readFileSync(file));
});
(async () => {
  await new Promise(r => server.listen(0,'127.0.0.1',r));
  const browser = await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
  try {
    for (const width of [1440,390]) {
      const page = await browser.newPage({viewport:{width,height:1050}});
      const errors=[], requests=[]; let tasks=[]; let offline=false;
      page.on('pageerror',e=>errors.push(e.message));
      const connection={id:1,name:'后台导入测试',driver:'acg_faka',base_url:'https://example.com',status:'active',settings:'{}',exchange_rate:1,auto_sync_price:true,stock_mode:'real'};
      const task={id:71,connection_id:1,mode:'selected',scope:'import',status:'processing',total:1168,processed:12,created:13,updated:0,manual_skipped:0,pending_count:1155,failed_count:0,retrying_count:0,stock_pending_count:8};
      await page.addInitScript(()=>localStorage.setItem('token',JSON.stringify('fixture')));
      await page.route('**/api/**', async route=>{
        const req=route.request(),url=new URL(req.url()),p=url.pathname;
        let data={items:[],total:0,categories:[],connections:[]};
        if(req.method()!=='GET')requests.push({p,data:req.postDataJSON()});
        if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'fixture'},permissions:['*']};
        if(p.endsWith('/captcha-config'))data={enabled:false};
        if(p.endsWith('/supply/connections'))data={connections:[connection],total:1};
        if(p.endsWith('/preview'))data={categories:[{code:'cat',name:'上游分类',products:[{code:'P1',name:'待导入商品',price_cents:-1,cost_price_cents:-1,quote_status:'pending',category_code:'cat',stock:-2,is_active:true}]}],total:1};
        if(p.endsWith('/import')){tasks=[task];data={task};}
        if(p.endsWith('/sync-tasks'))data={tasks,total:tasks.length};
        if(p.endsWith('/sync-tasks/71')){
          if(offline)return route.fulfill({status:503,json:{code:503,message:'进度暂不可用'}});
          data=task;
        }
        if(p.endsWith('/71/items'))data={total:1,items:[{code:'P1',name:'待导入商品',state:task.status==='done'?'failed':'pending',stage:'stock',saved:true,error_summary:'库存查询超时'}]};
        if(p.endsWith('/71/cancel')){Object.assign(task,{status:'canceled',cancel_requested_at:123,pending_count:1155});data=task;}
        if(p.endsWith('/71/retry')){Object.assign(task,{status:'processing',error_code:'',error_context:'',cancel_requested_at:0});data=task;}
        await route.fulfill({json:data});
      });
      const openTasks=async()=>{
        await page.getByRole('button',{name:/^(更多|操作)/}).first().click();
        await page.getByText('同步任务',{exact:true}).last().click();
      };
      await page.goto(`http://127.0.0.1:${server.address().port}/admin/channel`);
      await page.getByRole('button',{name:/^(更多|操作)/}).first().click();
      await page.getByText('导入商品',{exact:true}).last().click();
      const modal=page.locator('.n-modal').filter({hasText:'导入上游商品'});
      await expect(modal.getByRole('button',{name:'展开上游分类',exact:true})).toBeVisible();
      await modal.getByRole('button',{name:'展开上游分类',exact:true}).click();
      await modal.getByRole('checkbox',{name:/待导入商品/}).check();
      await modal.getByRole('button',{name:'开始导入',exact:true}).click();
      const progress=page.getByRole('region',{name:'商品导入进度'});
      await expect(progress).toBeVisible();
      await expect(progress.getByText('已保存 13',{exact:true})).toBeVisible();
      await expect(progress.getByText('待处理 1155',{exact:true})).toBeVisible();
      await expect(progress.getByText('导入失败 0',{exact:true})).toBeVisible();
      assert.match(requests.find(r=>r.p.endsWith('/import')).data.request_key,/^[a-f0-9-]{36}$/);
      assert.equal(requests.filter(r=>r.p.endsWith('/import')).length,1);
      await expect.poll(async () => { const b = await page.locator('.n-drawer').last().boundingBox(); return b && b.x >= -1 && b.x + b.width <= width + 1; }).toBeTruthy();
      await page.screenshot({path:`/tmp/zcard-import-task-${width}.png`,fullPage:true});
      await progress.getByRole('button',{name:'停止任务',exact:true}).click();
      await page.getByRole('button',{name:'停止后续处理',exact:true}).click();
      await expect(progress.getByRole('button',{name:'继续未完成商品',exact:true})).toBeVisible();
      await progress.getByRole('button',{name:'继续未完成商品',exact:true}).click();
      await expect(progress.getByRole('button',{name:'停止任务',exact:true})).toBeVisible();
      assert.equal(requests.filter(r=>r.p.endsWith('/import')).length,1,'resume resubmitted the import');
      Object.assign(task,{status:'done',processed:1168,pending_count:0,created:1160,manual_skipped:8,stock_pending_count:8,stock_failed_count:8,error_code:'PARTIAL_FAILED',error_context:'处理完成，8 件商品库存需要处理'});
      await expect(progress.getByRole('button',{name:'仅重试库存',exact:true})).toBeVisible({timeout:10000});
      await progress.getByRole('button',{name:'仅重试库存',exact:true}).click();
      assert.equal(requests.filter(r=>r.p.endsWith('/retry')).at(-1).data.scope,'stock');
      // Returning to the task list and reopening must use saved server state.
      await page.getByRole('button',{name:'返回任务列表',exact:true}).click();
      await page.getByRole('button',{name:'查看进度',exact:true}).click();
      await expect(progress.getByText('已保存 1160',{exact:true})).toBeVisible();
      offline=true;
      await expect(progress.getByText(/暂时无法读取进度/)).toBeVisible({timeout:10000});
      assert.equal(requests.filter(r=>r.p.endsWith('/import')).length,1);
      assert.equal(await page.locator('.n-message').filter({hasText:'进度暂不可用'}).count(),0,'polling must use inline recovery instead of repeated error toasts');
      offline=false;
      await expect(progress.getByText(/暂时无法读取进度/)).toHaveCount(0,{timeout:10000});
      Object.assign(task,{status:'failed',error_code:'CONFIG_CHANGED',error_context:'货源配置已变化'});
      await expect(progress.getByRole('button',{name:'确认配置后继续',exact:true})).toBeVisible({timeout:10000});
      await progress.getByRole('button',{name:'确认配置后继续',exact:true}).click();
      await page.getByRole('button',{name:'确认并继续',exact:true}).click();
      assert.equal(requests.filter(r=>r.p.endsWith('/retry')).at(-1).data.confirm_config,true);
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
      assert.deepEqual(errors,[]);
      await page.close(); console.log(`PASS durable import task UI ${width}px`);
    }
  } finally {await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
