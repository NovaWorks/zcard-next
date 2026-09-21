/** Production bundle regression for lost lazy chunks after a server upgrade.
 * pnpm --dir admin build
 * PLAYWRIGHT_MODULE=/path/to/playwright/test node scripts/test_admin_asset_recovery.cjs
 * --serve exposes only disposable local fixtures for interactive browser verification.
 */
const fs = require('node:fs'), path = require('node:path'), http = require('node:http');
const assert = require('node:assert/strict');
const root = process.env.ADMIN_DIST || path.resolve(__dirname, '../admin/dist');
let failNext = '', failed = 0;
const server = http.createServer((req, res) => {
  const p = new URL(req.url, 'http://localhost').pathname;
  res.setHeader('Cache-Control', 'no-store');
  if (p.startsWith('/__fixture__/arm/')) { failNext = p.split('/').pop(); res.end('armed'); return; }
  if (p === '/__fixture__/status') { res.setHeader('Content-Type','application/json'); res.end(JSON.stringify({failed,failNext})); return; }
  if (p.startsWith('/api/')) {
    let data = {items:[],total:0,levels:[],transactions:[],entries:[],categories:[],currencies:[],products:[],users:[],roles:[]};
    if(p.endsWith('/auth/profile')) data={admin:{id:1,username:'local-test'},permissions:['*']};
    res.setHeader('Content-Type','application/json'); res.end(JSON.stringify(data)); return;
  }
  const base = p.startsWith('/private/control/') ? '/private/control/' : '/admin/';
  const rel = p.slice(base.length);
  if (failNext && new RegExp(`^assets/${failNext}-[^/]+\\.js$`).test(rel)) {
    failNext=''; failed++; res.writeHead(404,{'Content-Type':'text/plain'}); res.end('missing old chunk'); return;
  }
  let file = path.join(root,rel);
  if(!fs.existsSync(file) || fs.statSync(file).isDirectory()) {
    if(rel.startsWith('assets/')) { res.writeHead(404,{'Content-Type':'text/plain'}); res.end('not found'); return; }
    file=path.join(root,'index.html');
  }
  let content=fs.readFileSync(file);
  if(path.extname(file)==='.html') content=content.toString().replaceAll('/admin/',base).replace('<head>','<head><script>localStorage.setItem("token",JSON.stringify("local-fixture"));localStorage.setItem("refreshToken",JSON.stringify("local-refresh"));</script>');
  res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png'})[path.extname(file)]||'application/octet-stream'); res.end(content);
});
(async()=>{
  await new Promise(r=>server.listen(0,'127.0.0.1',r));
  const base='http://127.0.0.1:'+server.address().port;
  console.log(base);
  if(process.argv.includes('--serve')) return;
  const {chromium,expect}=require(process.env.PLAYWRIGHT_MODULE||'@playwright/test');
  const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
  try {
    for (const [entry,chunk,title] of [['/admin/','marketing','营销管理'],['/private/control/','wallet','钱包管理']]) {
      const page=await browser.newPage({viewport:{width:390,height:850}});
      await page.goto(base+entry+'user');
      await expect(page.locator('main')).toContainText('用户管理');
      await fetch(base+'/__fixture__/arm/'+chunk);
      await page.getByRole('menuitem',{name:title,exact:true}).click();
      const alert=page.getByRole('alert',{name:'页面加载失败'});
      await expect(alert).toBeVisible();
      await expect(page.locator('#nprogress')).toHaveCount(0);
      await expect(page.locator('main')).toContainText('用户管理');
      await alert.getByRole('button',{name:'重新加载页面'}).click();
      await expect(page).toHaveURL(base+entry+chunk);
      await expect(page.locator('main')).toContainText(title);
      await expect(alert).toHaveCount(0);
      await expect(page.locator('main .n-spin-body')).toHaveCount(0);
      await page.close();
    }
    const page=await browser.newPage();
    await fetch(base+'/__fixture__/arm/marketing');
    await page.goto(base+'/private/control/marketing');
    await expect(page.getByRole('alert',{name:'页面加载失败'})).toBeVisible();
    await page.getByRole('button',{name:'重新加载页面'}).click();
    await expect(page.locator('main')).toContainText('营销管理');
    await expect(page.getByRole('alert',{name:'页面加载失败'})).toHaveCount(0);
    assert.equal(failed,3);
    console.log('PASS lost marketing/wallet chunks, custom admin entry, initial boot failure, target route recovery and no reload loop');
  }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
