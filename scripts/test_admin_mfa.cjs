/** Local-only MFA browser acceptance. Credentials and output paths are supplied
 * through environment variables; no production account is ever used. */
const fs = require('node:fs');
const crypto = require('node:crypto');
const assert = require('node:assert/strict');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = process.env.ZCARD_TEST_BASE_URL;
if (!base || !['127.0.0.1','localhost','[::1]'].includes(new URL(base).hostname)) throw Error('Local test server required');
const credentials = JSON.parse(fs.readFileSync(process.env.ZCARD_TEST_LOGIN_FILE,'utf8'));
const out = process.env.ZCARD_TEST_OUTPUT || '/tmp/zcard-mfa-browser'; fs.mkdirSync(out,{recursive:true});
function otp(secret, offset=0) {
 let bits=''; for(const c of secret.replace(/=+$/,'')) bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5,'0');
 const bytes=[]; for(let i=0;i+8<=bits.length;i+=8) bytes.push(parseInt(bits.slice(i,i+8),2));
 const msg=Buffer.alloc(8); msg.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)+offset));
 const h=crypto.createHmac('sha1',Buffer.from(bytes)).update(msg).digest(); const n=h[h.length-1]&15;
 return ((h.readUInt32BE(n)&0x7fffffff)%1000000).toString().padStart(6,'0');
}
async function api(path,data,token) {
 const response = await fetch(base+'/api/v1/admin/'+path,{method:data===undefined?'GET':'POST',headers:{'Content-Type':'application/json',...(token?{Authorization:'Bearer '+token}:{})},body:data===undefined?undefined:JSON.stringify(data)});
 return {status:response.status,body:await response.json(),headers:response.headers};
}
(async()=>{
 const browser=await chromium.launch({headless:true, channel: process.env.PLAYWRIGHT_CHANNEL || "chrome"});const context=await browser.newContext({viewport:{width:1360,height:1000}});const page=await context.newPage();const errors=[];
 page.on('pageerror',e=>errors.push(e.message));
 page.on('response',r=>{if(r.url().includes('/totp/'))console.log('MFA HTTP',r.status(),new URL(r.url()).pathname)});
 async function loginPassword(){await page.goto(base+'/admin/login');await page.getByPlaceholder('请输入用户名').fill(credentials.username);await page.getByPlaceholder('请输入密码').fill(credentials.password);const reply=page.waitForResponse(r=>r.url().endsWith('/auth/login')&&r.request().method()==='POST');await page.getByRole('button',{name:'确认',exact:true}).click();return (await reply).json();}
 try {
  let first=await loginPassword();assert(first.access_token);const oldToken=first.access_token;const oldRefresh=first.refresh_token;
  await page.waitForURL(url=>!url.pathname.includes('login'));
  await page.goto(base+'/admin/settings');await page.getByText('安全',{exact:true}).click();
  await page.getByRole('button',{name:'绑定谷歌验证器',exact:true}).click();
  await page.locator('.n-form-item').filter({hasText:'当前密码'}).locator('input').fill(credentials.password);
  let response=page.waitForResponse(r=>r.url().endsWith('/totp/enable'));await page.getByRole('button',{name:'生成绑定二维码'}).click();const setup=await(await response).json();assert(setup.secret&&setup.otpauth_url);
  assert(!Boolean((await api('auth/profile',undefined,oldToken)).body.admin.totp_enabled));
  await page.getByRole('button',{name:'取消绑定',exact:true}).click();
  assert.equal((await api('auth/totp/confirm',{code:otp(setup.secret)},oldToken)).status,400);
  await page.getByRole('button',{name:'绑定谷歌验证器',exact:true}).click();await page.locator('.n-form-item').filter({hasText:'当前密码'}).locator('input').fill(credentials.password);
  response=page.waitForResponse(r=>r.url().endsWith('/totp/enable'));await page.getByRole('button',{name:'生成绑定二维码'}).click();const binding=await(await response).json();
  await page.locator('.n-form-item').filter({hasText:'新验证器的 6 位动态码'}).locator('input').fill(otp(binding.secret,-1));
  response=page.waitForResponse(r=>r.url().endsWith('/totp/confirm'));await page.getByRole('button',{name:'确认绑定',exact:true}).click();const confirmed=await(await response).json();assert.equal(confirmed.recovery_codes.length,10);
  await page.getByRole('checkbox',{name:'我已安全保存恢复码'}).check();await page.getByRole('button',{name:'完成，返回登录'}).click();
  assert.equal((await api('auth/profile',undefined,oldToken)).status,401);
  assert.equal((await api('auth/refresh',{refresh_token:oldRefresh})).status,401);
  console.log('PASS bind/cancel/recovery display/old access and refresh revoked');
  let challenge=await loginPassword();assert(challenge.requires_totp&&!challenge.access_token&&!challenge.refresh_token);
  await page.getByPlaceholder('6 位动态验证码').fill('bad');await page.getByRole('button',{name:'确认',exact:true}).click();assert(await page.getByPlaceholder('6 位动态验证码').isVisible());
  await page.getByPlaceholder('6 位动态验证码').fill(otp(binding.secret));response=page.waitForResponse(r=>r.url().endsWith('/auth/login'));await page.getByRole('button',{name:'确认',exact:true}).click();let logged=await(await response).json();assert(logged.access_token);await page.waitForURL(url=>!url.pathname.includes('login'));
  await page.goto(base+'/admin/settings');await page.getByText('安全',{exact:true}).click();await page.getByText('已绑定 Google Authenticator',{exact:true}).waitFor();await page.waitForFunction(()=>[...document.querySelectorAll('.n-spin-content')].every(e=>getComputedStyle(e).opacity==='1'));await page.screenshot({path:out+'/security-desktop.png'});
  await page.setViewportSize({width:390,height:844});await page.reload();await page.getByText('安全',{exact:true}).click();await page.getByText('已绑定 Google Authenticator',{exact:true}).waitFor();await page.waitForFunction(()=>document.documentElement.scrollWidth<=innerWidth+1,{},{timeout:5000});await page.waitForFunction(()=>[...document.querySelectorAll('.n-spin-content')].every(e=>getComputedStyle(e).opacity==='1'));await page.screenshot({path:out+'/security-mobile.png'});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await page.setViewportSize({width:1360,height:1000});
  // Recovery login in a fresh context, then automatic account-security prompt.
  await context.clearCookies();await page.evaluate(()=>localStorage.clear());challenge=await loginPassword();assert(challenge.requires_totp);
  await page.getByRole('button',{name:'手机丢失？使用恢复码'}).click();await page.getByPlaceholder('20 位恢复码').fill(confirmed.recovery_codes[0]);response=page.waitForResponse(r=>r.url().endsWith('/auth/login'));await page.getByRole('button',{name:'确认',exact:true}).click();const recovered=await(await response).json();assert(recovered.recovery_ticket);await page.waitForURL(url=>!url.pathname.includes('login'));
  await page.getByRole('button',{name:'更换验证器 / 更新恢复码',exact:true}).click();await page.locator('.n-modal .n-form-item').filter({hasText:'当前密码'}).locator('input').fill(credentials.password);
  response=page.waitForResponse(r=>r.url().endsWith('/totp/enable'));await page.getByRole('button',{name:'生成绑定二维码'}).click();const replacement=await(await response).json();assert(replacement.secret);
  await page.locator('.n-form-item').filter({hasText:'新验证器的 6 位动态码'}).locator('input').fill(otp(replacement.secret,-1));response=page.waitForResponse(r=>r.url().endsWith('/totp/confirm'));await page.getByRole('button',{name:'确认绑定',exact:true}).click();const replaced=await(await response).json();assert.equal(replaced.recovery_codes.length,10);
  await page.getByRole('checkbox',{name:'我已安全保存恢复码'}).check();await page.getByRole('button',{name:'完成，返回登录'}).click();
  const expired=await api('auth/login',credentials);assert(expired.body.requires_totp);assert.equal((await api('auth/login',{challenge:expired.body.challenge,totp_code:confirmed.recovery_codes[1]})).status,400);
  const recovered2=await api('auth/login',{challenge:expired.body.challenge,totp_code:replaced.recovery_codes[0]});assert(recovered2.body.access_token);
  assert.equal((await api('auth/totp/disable',{password:credentials.password,code:replaced.recovery_codes[1]},recovered2.body.access_token)).status,200);
  assert.equal((await api('auth/profile',undefined,recovered2.body.access_token)).status,401);
  assert((await api('auth/login',credentials)).body.access_token);
  console.log('PASS OTP login/recovery login/rebind/old recovery invalid/disable/mobile');
  await page.evaluate(()=>localStorage.clear());const operator=await loginPassword();await page.waitForURL(url=>!url.pathname.includes('login'));
  const staffCredentials={username:'mfa-staff-'+Date.now(),password:crypto.randomBytes(16).toString('hex')};
  const staffCreate=await api('admins',{...staffCredentials,role_id:2},operator.access_token);assert.equal(staffCreate.status,200);const staff=staffCreate.body.admin||staffCreate.body;
  let staffLogin=await api('auth/login',staffCredentials);assert(staffLogin.body.access_token);
  const staffSetup=await api('auth/totp/enable',{password:staffCredentials.password},staffLogin.body.access_token);assert(staffSetup.body.secret);
  const staffConfirm=await api('auth/totp/confirm',{code:otp(staffSetup.body.secret,-1)},staffLogin.body.access_token);assert.equal(staffConfirm.status,200);
  await page.goto(base+'/admin/staff');const row=page.locator('tr').filter({hasText:staffCredentials.username});await row.getByRole('button',{name:'⋯',exact:true}).click();await page.getByText('解绑 TOTP',{exact:true}).click();
  const resetModal=page.locator('.n-modal').filter({hasText:'重置两步验证'});
  await resetModal.locator('.n-form-item').filter({hasText:'你的当前密码'}).locator('input').fill('wrong-password');await resetModal.locator('.n-form-item').filter({hasText:'重置原因'}).locator('input').fill('本地验证手机丢失恢复');
  response=page.waitForResponse(r=>r.url().endsWith('/totp/reset'));await resetModal.getByRole('button',{name:'确认强制重置'}).click();assert.equal((await response).status(),400);
  await resetModal.locator('.n-form-item').filter({hasText:'你的当前密码'}).locator('input').fill(credentials.password);response=page.waitForResponse(r=>r.url().endsWith('/totp/reset'));await resetModal.getByRole('button',{name:'确认强制重置'}).click();assert.equal((await response).status(),200);
  staffLogin=await api('auth/login',staffCredentials);assert(staffLogin.body.access_token);
  assert.equal((await api('auth/totp/reset',{admin_id:operator.admin.id,password:staffCredentials.password,reason:'must deny'},staffLogin.body.access_token)).status,403);
  const legacy=await fetch(base+'/api/v1/admin/admins/'+staff.id+'/totp-reset',{method:'PUT',headers:{'Content-Type':'application/json',Authorization:'Bearer '+operator.access_token},body:'{}'});assert.equal(legacy.status,400);
  console.log('PASS staff reset UI/operator password required/permission denial/legacy bypass blocked');
  assert.deepEqual(errors,[]);console.log('PASS no browser runtime errors');
 }catch(e){await page.screenshot({path:out+'/failure.png'}).catch(()=>{});console.error('browser errors',errors);throw e}finally{await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
