// Real localhost fullstack acceptance. All credentials/artifacts are ephemeral.
const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {spawn}=require('node:child_process');
const base=process.env.ZCARD_TEST_BASE_URL,root=process.env.ZCARD_TEST_FIXTURE;
if(!base||!['127.0.0.1','localhost'].includes(new URL(base).hostname)||!root)throw Error('isolated fixture required');
const id='member-purchase-gate',statePath=path.join(root,'p4-state.json');
const login=JSON.parse(fs.readFileSync(path.join(root,'login.json')));
async function call(url,data,token='',method=data?'POST':'GET'){
 const start=performance.now();const r=await fetch(base+url,{method,headers:{'Content-Type':'application/json',...(token?{Authorization:`Bearer ${token}`}:{})},body:data===undefined?undefined:JSON.stringify(data)});
 return {status:r.status,body:await r.json(),ms:performance.now()-start};
}
(async()=>{
 const auth=await call('/api/v1/admin/auth/login',login);assert.equal(auth.status,200);const token=auth.body.token||auth.body.access_token;
 const api=async(url,data,method)=>{const r=await call('/api/v1/admin'+url,data,token,method);assert.equal(r.status,200,JSON.stringify(r.body));return r.body};
 const purchase=(p,token='')=>call('/api/v1/storefront/orders',{items:p.map(product_id=>({product_id,quantity:1})),query_password:'p4-acceptance',contact:'p4@example.test'},token);
 if(process.env.ZCARD_P4_VERIFY){
  const s=JSON.parse(fs.readFileSync(statePath));
  const denied=await purchase([s.products[0]],s.denied);assert.equal(denied.status,process.env.ZCARD_P4_VERIFY==='missing'?503:403);assert.equal(denied.body.reason,process.env.ZCARD_P4_VERIFY==='missing'?'PLUGIN_UNAVAILABLE':'MEMBER_LEVEL_DENIED');
  const good=await purchase([s.products[0]],s.allowed);assert.equal(good.status,process.env.ZCARD_P4_VERIFY==='missing'?503:200,JSON.stringify(good.body));
  assert.equal((await purchase([s.products[5]])).status,200);
  assert.equal((await api(`/products/${s.products[0]}/plugin-rules/${id}`)).required,true);
  console.log('PASS persisted rules',process.env.ZCARD_P4_VERIFY);return;
 }
 const products=[];const cat=await api('/categories',{name:'P4 isolated'});
 for(let i=0;i<6;i++)products.push((await api('/products',{name:`P4-${i}`,category_id:cat.id,price_cents:1000,stock_type:'url',direct_content:'https://example.test/p4',stock_visible:true,status:1})).id);
 const level=await api('/member-levels',{name:'P4 level',threshold_type:'recharge',acquire_mode:'manual',display_mode:'hidden',discount:100,enabled:true});
 const users=[];
 for(const name of ['allowed','denied']){const u=await api('/users',{username:'p4-'+name,password:login.password});if(name==='allowed')await api(`/users/${u.id}/member-level`,{level_id:level.id,reason:'P4 fixture'},'PUT');users.push((await call('/api/v1/storefront/user/login',{username:u.username,password:login.password})).body.access_token)}
 const metrics=[];
 async function measure(name,ids,buyer,count=100){const samples=[];for(let i=0;i<count;i++){const r=await purchase(ids,buyer);assert.equal(r.status,200,JSON.stringify(r.body));samples.push(r.ms)};samples.sort((a,b)=>a-b);const total=samples.reduce((a,b)=>a+b,0);const p95=samples[Math.floor(count*.95)];assert(p95<1000,`${name} p95 ${p95} exceeds 1000ms`);metrics.push({name,count,p50_ms:samples[Math.floor(count*.5)],p95_ms:p95,max_ms:samples[count-1],requests_per_second:count*1000/total,errors:0});}
 await measure('no-plugin',[products[5]],users[0]);
 async function cli(args){const cmd=process.env.ZCARD_TEST_BINARY;if(!cmd)throw Error('binary required');await new Promise((resolve,reject)=>{const child=spawn(cmd,['plugin',...args,'--conf',path.join(root,'conf')],{cwd:root,stdio:['ignore','pipe','pipe']});let output='';child.stdout.on('data',b=>output+=b);child.stderr.on('data',b=>output+=b);child.on('error',reject);child.on('exit',code=>code===0?resolve():reject(Error(output)))})}
 const desc=v=>JSON.parse(fs.readFileSync(path.join(root,v,'descriptor.json')));
 const upload=(v,action,generation)=>cli(['import','--action',action,'--id',id,'--digest',desc(v).archiveSHA256,'--expected-generation',String(generation),'--descriptor',path.join(root,v,'descriptor.json'),'--signature',path.join(root,v,'signature.ed25519'),'--archive',path.join(root,v,'plugin.zplug'),'--approve-scopes']);
 await upload('v1','import',0);
 await cli(['enable','--id',id,'--expected-generation','1','--digest',desc('v1').archiveSHA256,'--approve-scopes']);
 for(const p of products.slice(0,5))await api(`/products/${p}/plugin-rules/${id}`,{expected_generation:'2',expected_config_revision:'0',expected_requirement_revision:'0',schema_version:1,config:{schema_version:1,revision:'0',enabled:true,allowed_level_ids:[String(level.id)]}},'PUT');
 await measure('single-rule',[products[0]],users[0]);await measure('enabled-unrestricted',[products[5]],users[0]);await measure('five-item-cart',products.slice(0,5),users[0]);
 let switched=false;const generations=[];
 const workers=Array.from({length:4},async()=>{for(let i=0;i<25;i++){const r=await purchase([products[0]],users[i%2]);assert.equal(r.status,i%2?403:200,JSON.stringify(r.body));if(i%2)assert.equal(r.body.reason,'MEMBER_LEVEL_DENIED');generations.push(switched?'after':'during')}});
 await upload('v2','upgrade',2);switched=true;await Promise.all(workers);
 assert.equal((await api(`/plugins/${id}`)).observed_digest,desc('v2').archiveSHA256);
 await upload('v1','rollback',3);
 await cli(['disable','--id',id,'--expected-generation','4']);
 assert.equal((await purchase([products[0]],users[0])).body.reason,'PLUGIN_UNAVAILABLE');assert.equal((await purchase([products[5]])).status,200);
 await cli(['enable','--id',id,'--expected-generation','5','--digest',desc('v1').archiveSHA256,'--approve-scopes']);
 fs.writeFileSync(statePath,JSON.stringify({products,allowed:users[0],denied:users[1],digest:desc('v1').archiveSHA256}));
 fs.writeFileSync(path.join(root,'p4-performance.json'),JSON.stringify({metrics,concurrent_requests:generations.length,p95_budget_ms:1000,redis:false},null,2));
 console.log('PASS HTTP performance / concurrent same-policy upgrade / rollback / live CLI disable-enable',JSON.stringify(metrics));
})().catch(e=>{console.error(e);process.exitCode=1});
