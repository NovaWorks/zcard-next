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
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
 try {
  for(const width of [1440,390]) {
   const page=await browser.newPage({viewport:{width,height:1000}});page.setDefaultTimeout(12000);
   const errors=[],writes=[];let failSave=false;
   const categories=[{id:10,name:'账号专区',parent_id:0,sort:0,status:1},{id:11,name:'其他商品',parent_id:0,sort:1,status:1}];
   const rules=[{keywords:['小火箭'],excludes:['独享'],category_id:10,match_all:false}];
   const connection={id:1,name:'测试货源',driver:'acg_faka',base_url:'https://example.test',status:'active',exchange_rate:1,settings:JSON.stringify({category_rules:rules})};
   const product={id:1,name:'小火箭共享账号',category_id:11,price_cents:1000,factory_price_cents:500,stock_type:'card',status:1,fulfillment_mode:'auto',stock:9,upstream_source_id:1,upstream_product_code:'p1'};
   const products=[{code:'p1',name:'小火箭共享账号',already_imported:true},{code:'p2',name:'小火箭独享账号'},{code:'p3',name:'游戏充值'},{code:'p4',name:'小火箭锁定商品',is_locked:true}].map(p=>({...p,stock:9,price_cents:1000,cost_price_cents:500,quote_status:'ready',is_active:true}));
   let delivery={sources:[{sku_id:1,sku_name:'共享规格',mode:'upstream',status:'unconfigured'},{sku_id:2,sku_name:'独享规格',mode:'upstream',status:'unconfigured'}],receipts:[{procurement_id:7,content_index:0,sku_id:1,label:'采购 #7 · 第1份'}],revision:0};
   await page.addInitScript(()=>localStorage.setItem('token',JSON.stringify('fixture')));
   page.on('pageerror',e=>errors.push(e.message));
   await page.route('**/api/**',async route=>{
    const req=route.request(),p=new URL(req.url()).pathname,write=req.method()!=='GET';if(write)writes.push({p,data:req.postDataJSON()});
    let data={items:[],total:0,categories:[],connections:[],skus:[],controls:[]};
    if(p.endsWith('/auth/profile'))data={admin:{id:1,username:'fixture'},permissions:['*']};
    if(p.endsWith('/captcha-config'))data={enabled:false};
    if(p.endsWith('/categories'))data={categories};
    if(p.endsWith('/supply/connections'))data={connections:[connection],total:1};
    if(p.endsWith('/preview'))data={categories:[{code:'g',name:'上游分类',products}],total:4};
    if(p.endsWith('/import'))data={task:{id:71,scope:'import',status:'done',connection_id:1,total:3,processed:3,created:2,updated:1}};
    if(p.endsWith('/products'))data={products:[product],total:1};
    if(p.endsWith('/products/1'))data=product;
    if(p.endsWith('/delivery-sources')){
     if(write&&failSave)return route.fulfill({status:409,json:{code:409,message:'设置已变化，请刷新后重试'}});
     if(write){const v=req.postDataJSON();delivery.revision++;(()=>{const target=delivery.sources.find(s=>Number(s.sku_id)===Number(v.sku_id));if(!target)throw Error(JSON.stringify({requestKeys:Object.keys(v),sku:v.sku_id,skuId:v.skuId,known:delivery.sources.map(s=>s.sku_id)}));return Object.assign(target,{mode:v.mode,status:v.action==='pause'?'paused':'ready',has_content:true,id:9,max_deliveries:v.max_deliveries||0});})();}
     data=delivery;
    }
    if(p.endsWith('/classify')){const v=req.postDataJSON();data={items:[{id:1,name:product.name,category_id:11,revision:'r1',status:v.apply?'updated':'matched'}],skipped_locked:1,skipped_protected:2,updated:v.apply?1:0};}
    await route.fulfill({json:data});
   });
   const base=`http://127.0.0.1:${server.address().port}`;
   try {
    await page.goto(base+'/admin/channel');await page.getByRole('button',{name:/^(更多|操作)/}).first().click();await page.getByText('导入商品',{exact:true}).last().click();
    const modal=page.locator('.n-modal').filter({hasText:'导入上游商品'});
    await modal.getByRole('button',{name:/全选全部商品/}).click();
    await modal.getByRole('button',{name:'展开上游分类',exact:true}).click();
    await expect(modal.getByRole('checkbox',{name:/小火箭锁定商品/})).toBeDisabled();
    await expect(modal.getByRole('checkbox',{name:/游戏充值/})).toBeChecked();
    await modal.getByRole('button',{name:'清空选择',exact:true}).click();
    await modal.getByPlaceholder('搜索上游分类或商品名称').fill('小火箭');
    await expect(modal.getByText('游戏充值',{exact:true})).toHaveCount(0);
    await modal.getByRole('button',{name:/全选搜索结果/}).click();
    await modal.getByPlaceholder('搜索上游分类或商品名称').fill('');
    await expect(modal.getByRole('checkbox',{name:/游戏充值/})).not.toBeChecked();
    await modal.getByRole('button',{name:'仅选未导入商品',exact:true}).click();
    await expect(modal.getByRole('checkbox',{name:/小火箭共享账号/})).not.toBeChecked();
    await modal.getByRole('button',{name:/全选全部商品/}).click();
    await modal.getByRole('button',{name:/自动分类/}).click();
    await expect(modal.locator('.rule-preview-row').filter({hasText:'小火箭共享账号'})).toContainText('规则 1');
    await expect(modal.locator('.rule-preview-row').filter({hasText:'小火箭独享账号'})).toContainText('未命中');
    await page.screenshot({path:`/tmp/zcard-import-selection-${width}.png`,fullPage:true});
    assert(await modal.locator('.import-body').evaluate(el=>el.scrollWidth<=el.clientWidth+1),'import horizontal overflow');
    await modal.getByRole('button',{name:'开始导入',exact:true}).click();
    await expect.poll(()=>writes.filter(r=>r.p.endsWith('/import')).length).toBe(1);
    const payload=writes.find(r=>r.p.endsWith('/import')).data;assert.deepEqual(payload.codes.sort(),['p1','p2','p3']);assert.equal(payload.selected_categories_only,true);assert.equal(Number(payload.category_rules[0].category_id),10);
    await page.goto(base+'/admin/product');
    await page.getByRole('button',{name:'发货设置',exact:true}).first().click();
    const source=page.locator('.n-modal').filter({hasText:'商品发货设置'});await expect(source.getByText('共享规格',{exact:true})).toBeVisible();
    await source.locator('.n-form-item').filter({hasText:'发货来源'}).locator('.n-base-selection').click();await page.getByText('重复发货：复用同一份内容',{exact:true}).last().click();
    await source.locator('.n-form-item').filter({hasText:'内容来源'}).locator('.n-base-selection').click();await page.getByText('填写自己的账号 / 卡密',{exact:true}).last().click();
    const secret=source.getByPlaceholder('账号、密码及使用说明；内容加密保存，仅发给已付款买家');await secret.fill('fixture-account');
    failSave=true;await source.getByRole('button',{name:'保存发货设置',exact:true}).click();await expect(source.getByText('设置已变化，请刷新后重试',{exact:false})).toBeVisible();await expect(secret).toHaveValue('fixture-account');
    failSave=false;await source.getByRole('button',{name:'保存发货设置',exact:true}).click();await expect(source.getByText('可重复发放',{exact:true})).toBeVisible();
    await source.getByRole('button',{name:'暂停复用，停止新销售',exact:true}).click();await expect(source.getByText('已暂停',{exact:true})).toBeVisible();
    await page.screenshot({path:`/tmp/zcard-delivery-settings-${width}.png`,fullPage:true});
    assert(await source.locator('.delivery-settings').evaluate(el=>el.scrollWidth<=el.clientWidth+1),'delivery horizontal overflow');
    await source.getByRole('button',{name:'关闭',exact:true}).click();
    await page.getByRole('button',{name:'按关键词分类',exact:true}).click();
    const classify=page.locator('.n-modal').filter({hasText:'按关键词自动分类'});
    await classify.getByPlaceholder('例如：小火箭，Shadowrocket，Apple ID').fill('小火箭');
    await classify.locator('.n-base-selection').last().click();await page.locator('.n-base-select-option').filter({hasText:'账号专区'}).click();
    await classify.getByRole('button',{name:'预览匹配商品',exact:true}).click();await expect(classify.getByText(/将修改 1 件/)).toBeVisible();
    await classify.getByRole('button',{name:'确认分类 1 件商品',exact:true}).click();await expect(classify.getByText(/已修改 1 件/)).toBeVisible();
    assert.equal(writes.filter(r=>r.p.endsWith('/classify')).at(-1).data.revisions['1'],'r1');
    await page.screenshot({path:`/tmp/zcard-classification-${width}.png`,fullPage:true});
    assert.deepEqual(errors,[]);
   }catch(e){await page.screenshot({path:`/tmp/zcard-optimization-failure-${width}.png`,fullPage:true});fs.writeFileSync(`/tmp/zcard-optimization-failure-${width}.txt`,await page.locator('body').innerText());throw e;}finally{await page.close();}
  }
  console.log('PASS desktop/mobile: select all, filtered selection, unimported selection, locked skips, rules/exclusions, delivery save error retention, per-SKU configuration, pause, existing-product classification');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1});
