(()=>{
const token=new URL(location.href).searchParams.get('theme_preview');
if(!token)return;
const readOnly=(method)=>['GET','HEAD','OPTIONS'].includes(String(method||'GET').toUpperCase());
const original=window.fetch.bind(window);
window.fetch=(input,init={})=>{
 const method=init.method||(input instanceof Request?input.method:'GET');
 if(!readOnly(method))return Promise.resolve(new Response(JSON.stringify({message:'主题预览不支持下单、充值或修改数据',reason:'theme.PREVIEW_READ_ONLY'}),{status:403,headers:{'Content-Type':'application/json'}}));
 const url=new URL(input instanceof Request?input.url:input,location.href);
 if(url.origin===location.origin&&url.pathname.startsWith('/api/')){const headers=new Headers(init.headers||(input instanceof Request?input.headers:undefined));headers.set('X-Theme-Preview',token);init={...init,headers};}
 return original(input,init);
};
const open=XMLHttpRequest.prototype.open;
XMLHttpRequest.prototype.open=function(method,...args){if(!readOnly(method))throw new Error('主题预览为只读');return open.call(this,method,...args)};
addEventListener('submit',e=>e.preventDefault(),true);
function keepPreview(value){if(!value)return value;const u=new URL(value,location.href);if(u.origin!==location.origin)return value;u.searchParams.set('theme_preview',token);return u.pathname+u.search+u.hash}
for(const name of ['pushState','replaceState']){const fn=history[name].bind(history);history[name]=(state,title,url)=>fn(state,title,keepPreview(url));}
addEventListener('click',e=>{const a=e.target.closest?.('a');if(!a)return;const u=new URL(a.href,location.href);if(u.origin!==location.origin){e.preventDefault();return}if(a.target==='_blank'){e.preventDefault();return}a.href=keepPreview(a.href)},true);
})();
