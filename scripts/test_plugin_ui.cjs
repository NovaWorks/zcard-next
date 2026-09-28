// Real signed-package/browser acceptance. Run only against a fresh isolated localhost fixture.
// ZCARD_TEST_BASE_URL, ZCARD_TEST_FIXTURE (scripts/fixtures/plugin-ui.go),
// PLAYWRIGHT_MODULE and CHROME_PATH may override the local browser installation.
const fs = require('node:fs'), path = require('node:path'), assert = require('node:assert/strict');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = process.env.ZCARD_TEST_BASE_URL;
if (!base || !['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname)) throw Error('Isolated localhost fixture required');
const fixture = process.env.ZCARD_TEST_FIXTURE;
if (!fixture || !fs.existsSync(path.join(fixture, 'key.pem'))) throw Error('Signed fixture required');
const output = path.join(fixture, 'browser'); fs.mkdirSync(output, { recursive: true });
const id = 'member-purchase-gate';
const login = JSON.parse(fs.readFileSync(path.join(fixture, 'login.json')));
async function json(url, data, method = data ? 'POST' : 'GET', token = '') {
  const r = await fetch(base + url, { method, headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) }, body: data === undefined ? undefined : JSON.stringify(data) });
  const text = await r.text(); let body; try { body=JSON.parse(text); } catch { throw Error(`${url}: non-JSON response HTTP ${r.status}`); } return { status: r.status, body };
}
const shown = locator => locator.waitFor({ state: 'visible' });
(async () => {
 const auth = await json('/api/v1/admin/auth/login', login); assert.equal(auth.status, 200);
 const token = auth.body.token || auth.body.access_token;
 const api = async (url, data, method) => { const r = await json('/api/v1/admin' + url, data, method, token); assert.equal(r.status, 200, `${url}: ${JSON.stringify(r.body)}`); return r.body; };
 const stamp = Date.now();
 const level = await api('/member-levels', { name: `P3等级${stamp}`, threshold_type: 'recharge', acquire_mode: 'manual', display_mode: 'hidden', discount: 100, enabled: true });
 const category = await api('/categories', { name: `P3分类${stamp}` });
 const products = [];
 for (const letter of ['A','B']) products.push(await api('/products', { name: `P3商品${letter}${stamp}`, category_id: category.id, price_cents: 1000, stock_type: 'url', direct_content: 'https://example.com/p3', stock_visible: true, status: 1 }));
 const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) });
 const page = await browser.newPage({ viewport: { width: 1440, height: 1050 } }); page.setDefaultTimeout(15000);
 const errors = []; page.on('pageerror', e => errors.push(e.message));
 const adminPath = process.env.ZCARD_TEST_ADMIN_PATH || '/p3-admin';
 await page.addInitScript(t => localStorage.setItem('token', JSON.stringify(t)), token);
 async function confirm() { await page.locator('.n-dialog').getByRole('button', { name: '确认执行', exact: true }).click(); await page.locator('.n-dialog').waitFor({ state: 'hidden' }); }
 async function upload(version, action) {
   const label = { import:'上传签名包', upgrade:'升级', rollback:'回滚' }[action];
   await page.getByRole('button', { name: label, exact: true }).click();
   const modal = page.locator('.n-modal').filter({ has: page.getByText('描述文件 descriptor.json', { exact: true }) });
   for (const [name, file] of [['描述文件 descriptor.json','descriptor.json'], ['签名文件 signature.ed25519','signature.ed25519'], ['插件包 plugin.zplug','plugin.zplug']]) await modal.getByLabel(name, { exact: true }).setInputFiles(path.join(fixture, version, file));
   await modal.getByRole('button', { name: '验证并预览', exact: true }).click();
   await shown(modal.getByText('签名与兼容性验证通过', { exact: true }));
   await modal.getByRole('checkbox').check();
   await modal.getByRole('button', { name: { import:'安装（暂不激活）', upgrade:'升级', rollback:'回滚' }[action], exact: true }).click();
   await confirm(); await modal.waitFor({state:"hidden"}); await shown(page.getByText('操作已确认；以下展示最新实际状态。', { exact: true })); console.log("PASS upload",action,version);
 }
 async function productEditor(p) {
   await page.goto(base + adminPath + '/product');
   const row = page.locator('tr.n-data-table-tr').filter({ has: page.getByText(p.name, { exact: true }) });
   await row.getByRole('button', { name: /^(编辑|查看)$/ }).click();
   await page.locator('.n-step').filter({ hasText: '插件扩展' }).click();
   await shown(page.getByTestId('plugin-extensions').getByText(id, { exact: true }));
 }
 try {
   await page.goto(base + adminPath + '/plugin');
   await upload('v1','import');
   await page.getByRole('button', { name:'激活', exact:true }).click(); await confirm();
   await shown(page.getByText('运行中', { exact:true }));
   await page.goto(base+adminPath+'/product');
   await page.getByRole('button',{name:'新增商品',exact:true}).click();
   await page.locator('.n-step').filter({hasText:'插件扩展'}).click();
   await shown(page.getByText('请先保存商品，获得真实商品 ID 后再配置扩展。',{exact:true}));
   assert.equal(await page.getByTestId('plugin-extensions').getByRole('button',{name:'保存扩展设置',exact:true}).count(),0);
   await page.locator('.n-modal').getByRole('button',{name:'取消',exact:true}).click();
   await productEditor(products[0]);
   const panel = page.getByTestId('plugin-extensions');
   await panel.getByRole('switch').click();
   await panel.locator('.n-select').click();
   await page.getByText(level.name, { exact:true }).click();
   await panel.getByRole('button', {name:'保存扩展设置',exact:true}).click();
   await shown(panel.getByText('扩展设置已单独保存并生效',{exact:true}));
   const rule = await api(`/products/${products[0].id}/plugin-rules/${id}`);
   assert.equal(rule.required,true); assert.deepEqual(rule.config.allowed_level_ids,[String(level.id)]);
   const untouched = await api(`/products/${products[1].id}/plugin-rules/${id}`); assert.equal(!!untouched.required,false);
   await page.screenshot({path:path.join(output,'product-desktop.png'),fullPage:true,animations:"disabled"});
   const renewed=await json('/api/v1/admin/auth/login',login);assert.equal(renewed.status,200);
   await page.evaluate(t=>localStorage.setItem('token',JSON.stringify(t)),renewed.body.token||renewed.body.access_token);
   await page.reload(); await productEditor(products[0]);
   assert.equal(await panel.getByRole('switch').getAttribute('aria-checked'),'true');
   // A stale CAS must retain the local draft and show the new server configuration.
   await panel.getByRole('switch').click();
   await api(`/products/${products[0].id}/plugin-rules/${id}`, { config:rule.config, expected_generation:rule.generation, expected_config_revision:rule.config.revision, expected_requirement_revision:rule.requirement_revision, schema_version:1 }, 'PUT');
   await panel.getByRole('button',{name:'保存扩展设置',exact:true}).click();
   await shown(panel.getByText('服务端版本或配置已变化，草稿已保留。请比较后选择如何继续。',{exact:true}));
   assert.equal(await panel.getByRole('switch').getAttribute('aria-checked'),'false');
   await panel.getByRole('button',{name:'采用服务端设置',exact:true}).click();
   assert.equal(await panel.getByRole('switch').getAttribute('aria-checked'),'true');
   // Failed reads must not manufacture a disabled config or erase an edited draft.
   await panel.getByRole('switch').click();
   const configURL=`**/api/v1/admin/products/${products[0].id}/plugin-rules*`;
   await page.route(configURL, route=>route.abort('failed'));
   await panel.getByRole('button',{name:'刷新状态',exact:true}).click();
   await shown(panel.getByText('加载失败，当前内容可能已过期；请重试。',{exact:true}));
   assert.equal(await panel.getByRole('switch').getAttribute('aria-checked'),'false');
   assert.equal(await panel.getByRole('button',{name:'保存扩展设置',exact:true}).isDisabled(),true);
   await page.unroute(configURL);
   const schemaURL='**/api/v1/admin/plugins/contributions?*';
   await page.route(schemaURL,route=>route.fulfill({status:503,json:{reason:'PLUGIN_UNAVAILABLE'}}));
   await panel.getByRole('button',{name:'刷新状态',exact:true}).click();
   await page.waitForFunction(()=>!document.querySelector('[data-testid=plugin-extensions] .n-button--loading'));
   await shown(panel.getByText('加载失败，当前内容可能已过期；请重试。',{exact:true}));
   await panel.getByRole('button',{name:'单独解除购买限制',exact:true}).focus();
   assert.equal(await panel.getByRole('button',{name:'单独解除购买限制',exact:true}).isDisabled(),false);
   await page.unroute(schemaURL);
   await page.route(schemaURL, async route=>{const response=await route.fetch();const body=await response.json();body.contributions[0].ui_schema_json='[{"extensionPoint":"admin.product.edit.extra.v1","fields":[{"type":"html"}]}]';await route.fulfill({response,json:body});});
   await panel.getByRole('button',{name:'刷新状态',exact:true}).click();
   await shown(panel.getByText('服务端版本或配置已变化，草稿已保留。请比较后选择如何继续。',{exact:true}));
   assert.equal(await panel.getByRole('button',{name:'基于最新版本继续编辑草稿',exact:true}).isDisabled(),true);
   assert.equal(await panel.getByRole('switch').getAttribute('aria-checked'),'false');
   await page.unroute(schemaURL);
   await panel.getByRole('button',{name:'采用服务端设置',exact:true}).click();
   await shown(panel.getByText('不支持此配置格式或控件，已禁止保存；现有购买限制不会自动解除。',{exact:true}));
   await panel.getByRole('button',{name:'刷新状态',exact:true}).click();
   await shown(panel.getByRole('switch'));
   const deniedURL=`**/api/v1/admin/products/${products[0].id}/plugin-rules/${id}`;
   await panel.getByRole('switch').click();
   await page.route(deniedURL, route=>route.request().method()==='PUT'?route.fulfill({status:403,json:{reason:'plugin.FORBIDDEN',message:'权限不足'}}):route.continue());
   await panel.getByRole('button',{name:'保存扩展设置',exact:true}).click();
   await shown(panel.getByText('保存未确认，草稿已保留。请刷新核对权限、商品锁和服务端配置后重试。',{exact:true}));
   assert.equal(await panel.getByRole('switch').getAttribute('aria-checked'),'false');
   await page.unroute(deniedURL);
   // Closing must ask before discarding the unsaved extension draft.
   await page.locator('.n-modal').last().locator('.n-base-close').first().click();
   await shown(page.getByText('放弃未保存修改？',{exact:true}));
   await page.locator('.n-dialog').getByRole('button',{name:'继续编辑',exact:true}).click();
   assert.equal(await panel.getByRole('switch').getAttribute('aria-checked'),'false');
   await panel.getByRole('switch').click();
   let productState=await api(`/products/${products[0].id}`);
   await api(`/products/${products[0].id}/lock`,{is_locked:true,expected_version:productState.lock_version||0},'PUT');
   await productEditor(products[0]);
   await shown(panel.getByText('商品已锁定。请先在商品管理中按权限解锁。',{exact:true}));
   assert.equal(await panel.getByRole('button',{name:'保存扩展设置',exact:true}).isDisabled(),true);
   assert.equal(await panel.getByRole('button',{name:'单独解除购买限制',exact:true}).isDisabled(),true);
   const lockedRule=await api(`/products/${products[0].id}/plugin-rules/${id}`);
   const lockDenied=await json(`/api/v1/admin/products/${products[0].id}/plugin-rules/${id}/release`,{expected_requirement_revision:lockedRule.requirement_revision},'POST',token);
   assert.notEqual(lockDenied.status,200);
   productState=await api(`/products/${products[0].id}`);
   await api(`/products/${products[0].id}/lock`,{is_locked:false,expected_version:productState.lock_version||0},'PUT');
   // Browser-configured rule must enforce real public order creation for real users.
   const users=[];
   for (const name of ['allowed','denied']) {
     const user=await api('/users',{username:`p3${name}${stamp}`,password:login.password});
     if(name==='allowed') await api(`/users/${user.id}/member-level`,{level_id:level.id,reason:'P3 isolated UI acceptance'},'PUT');
     const r=await json('/api/v1/storefront/user/login',{username:user.username,password:login.password});assert.equal(r.status,200);users.push(r.body.access_token);
   }
   const order={items:[{product_id:products[0].id,quantity:1}],query_password:'test-p3',contact:'p3@example.com'};
   const allowed=await json('/api/v1/storefront/orders',order,'POST',users[0]);assert.equal(allowed.status,200,JSON.stringify(allowed.body));
   const denied=await json('/api/v1/storefront/orders',order,'POST',users[1]);assert.equal(denied.status,403);assert.equal(denied.body.reason,'MEMBER_LEVEL_DENIED');
   const guest=await json('/api/v1/storefront/orders',order);assert.equal(guest.body.reason,'LOGIN_REQUIRED');
   const shopper=await browser.newPage({viewport:{width:390,height:900}});
   await shopper.addInitScript(t=>localStorage.setItem('zcard_token',t),users[1]);
   await shopper.goto(base+`/product/${products[0].id}`);
   await shopper.getByPlaceholder('用于取货验证（忘记将无法取货）').fill('keep-my-password');
   await shopper.locator('.pd-actions').getByRole('button',{name:'立即购买',exact:true}).click();
   await shown(shopper.getByRole('alert').filter({hasText:'当前账号不符合此商品的购买条件，请联系商家。'}));
   assert.equal(await shopper.getByPlaceholder('用于取货验证（忘记将无法取货）').inputValue(),'keep-my-password');
   assert(!(await shopper.locator('body').innerText()).includes(level.name));
   const cartResult=await json('/api/v1/storefront/cart/items',{product_id:products[0].id,quantity:1},'POST',users[1]);assert.equal(cartResult.status,200,JSON.stringify(cartResult.body));
   await shopper.goto(base+'/cart');
   await shopper.getByPlaceholder(/查询密码/).fill('keep-cart-password');
   await shopper.getByRole('button',{name:/去结算/}).click();
   await shown(shopper.getByRole('alert').filter({hasText:'当前账号不符合此商品的购买条件，请联系商家。'}));
   assert.equal(await shopper.getByPlaceholder(/查询密码/).inputValue(),'keep-cart-password');
   assert.equal(await shopper.locator('.cart-item').count(),1);
   await shopper.screenshot({path:path.join(output,'storefront-denial-mobile.png'),fullPage:true,animations:'disabled'});
   await page.goto(base+adminPath+'/plugin');
   await upload('v2','upgrade');
   const v2=JSON.parse(fs.readFileSync(path.join(fixture,'v2/descriptor.json')));
   assert.equal((await api(`/plugins/${id}`)).observed_digest,v2.archiveSHA256);
   const operationIDs=[],queryIDs=[];
   const opURL=`**/api/v1/admin/plugins/${id}/operations`;
   await page.route(opURL,async route=>{operationIDs.push(route.request().postDataJSON().operation_id);await route.fetch();await route.abort('failed');});
   page.on('request', request=>{const match=new URL(request.url()).pathname.match(/plugins\/operations\/([^/]+)$/);if(match)queryIDs.push(match[1]);});
   await page.getByRole('button',{name:'停用',exact:true}).click(); await confirm();
   await shown(page.getByText('操作已确认；以下展示最新实际状态。',{exact:true}));
   assert.equal(operationIDs.length,1);assert(queryIDs.includes(operationIDs[0]));
   await page.unroute(opURL);
   assert.equal((await api(`/products/${products[0].id}/plugin-rules/${id}`)).required,true);
   const stopped=await json('/api/v1/storefront/orders',order,'POST',users[0]);assert.equal(stopped.body.reason,'PLUGIN_UNAVAILABLE');
   await shopper.getByRole('button',{name:/去结算/}).click();
   await shown(shopper.getByRole('alert').filter({hasText:'此商品的购买校验暂不可用，请稍后重试或联系商家。'}));
   assert.equal(await shopper.getByPlaceholder(/查询密码/).inputValue(),'keep-cart-password');
   const guestPage=await browser.newPage();
   await guestPage.goto(base+`/product/${products[0].id}`);
   await guestPage.getByPlaceholder('用于取货验证（忘记将无法取货）').fill('guest-password');
   await guestPage.getByPlaceholder(/用于订单查询与售后/).fill('guest@example.com');

   await page.setViewportSize({width:390,height:900});
   await page.getByRole('button',{name:/仍有限制的商品/}).click();
   await page.getByRole('button',{name:new RegExp(`商品 ${products[0].id} · 查看商品限制`)}).click();
   await shown(panel.getByText('宿主购买限制仍生效',{exact:true}));
   await page.waitForFunction(()=>document.querySelectorAll('.n-modal').length===1);
   await panel.getByRole('button',{name:'单独解除购买限制',exact:true}).focus();
   await page.screenshot({path:path.join(output,'retained-mobile.png'),fullPage:true,animations:"disabled"});
   const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1);assert.equal(overflow,false);
   const editorModal=page.locator('.n-modal').filter({has:panel});
   await editorModal.locator('.n-base-close').first().click(); await editorModal.waitFor({state:'hidden'});
   const impactModal=page.locator('.n-modal').filter({has:page.getByText('下方仅列当前站点商品；总数可能包含其他站点。',{exact:true})});
   await impactModal.locator('.n-base-close').first().click(); await impactModal.waitFor({state:'hidden'});
   await page.getByRole('button',{name:'激活',exact:true}).click(); await confirm(); await shown(page.getByText('运行中',{exact:true}));
   await guestPage.locator('.pd-actions').getByRole('button',{name:'立即购买',exact:true}).click();
   await shown(guestPage.getByRole('alert').filter({hasText:'此商品需要登录后购买，请先登录。'}));
   assert.equal(await guestPage.getByPlaceholder('用于取货验证（忘记将无法取货）').inputValue(),'guest-password');
   await guestPage.close(); await shopper.close();
   await upload('v1','rollback');
   assert.equal((await api(`/plugins/${id}`)).observed_digest,JSON.parse(fs.readFileSync(path.join(fixture,'v1/descriptor.json'))).archiveSHA256);
   await page.getByRole('button',{name:'卸载（保留规则）',exact:true}).click();await confirm();
   assert.equal((await api(`/products/${products[0].id}/plugin-rules/${id}`)).required,true);
   await page.getByRole('button',{name:/仍有限制的商品/}).click();
   await page.getByRole('button',{name:new RegExp(`商品 ${products[0].id} · 查看商品限制`)}).click();
   await panel.getByRole('button',{name:'单独解除购买限制',exact:true}).click();await confirm();
   await shown(panel.getByText('此插件没有宿主购买限制',{exact:true}));
   assert.equal(!!(await api(`/products/${products[0].id}/plugin-rules/${id}`)).required,false);
   const released=await json('/api/v1/storefront/orders',order,'POST',users[0]);assert.equal(released.status,200,JSON.stringify(released.body));
   const supportRole=(await api('/authz/roles')).roles.find(r=>r.code==='support');
   await api(`/authz/roles/${supportRole.id}/permissions`,{permissions:supportRole.permissions.filter(code=>code!=='plugin:read')},'PUT');
   for(const role of ['operator','support']) {
     const r=await json('/api/v1/admin/auth/login',{username:`p3-${role}`,password:login.password});assert.equal(r.status,200);
     const restrictedToken=r.body.token||r.body.access_token;
     const forbidden=await json('/api/v1/admin/plugins/inspect',{},'POST',restrictedToken);assert.equal(forbidden.status,403);
     const reader=await browser.newPage();await reader.addInitScript(t=>localStorage.setItem('token',JSON.stringify(t)),restrictedToken);
     await reader.goto(base+adminPath+'/plugin');
     if(role==='operator') {await shown(reader.getByText('仅主站实例管理员且拥有插件管理权限时，可安装和管理插件。',{exact:true}));assert.equal(await reader.getByRole('button',{name:'上传签名包',exact:true}).isDisabled(),true);}
     else {await reader.waitForURL(/403/);assert.equal((await json('/api/v1/admin/plugins',undefined,'GET',restrictedToken)).status,403);}
     await reader.close();
   }
   assert.deepEqual(errors,[]);
   fs.writeFileSync(path.join(fixture,'results.json'),JSON.stringify({base,adminPath,products:products.map(p=>p.id),level:level.id,output,tests:'signed inspect/import/enable/product isolation/CAS/real buyers/upgrade/disable/restore/rollback/uninstall/release/mobile/custom admin path'},null,2));
   console.log('PASS real plugin browser lifecycle, isolated products, CAS draft, real buyer gate, 390px recovery, custom admin path');
 } catch(e) { await page.screenshot({path:path.join(output,'failure.png'),fullPage:true,animations:"disabled"}); fs.writeFileSync(path.join(output,'failure.txt'),await page.locator('body').innerText()); throw e; }
 finally { await browser.close(); }
})().catch(e=>{console.error(e);process.exitCode=1;});
