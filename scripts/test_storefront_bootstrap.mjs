/** Pure module regression; no browser, production account, or external API required.
 * node scripts/test_storefront_bootstrap.mjs
 */
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFileSync, mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
const require = createRequire(import.meta.url);
const { build } = createRequire(require.resolve('../storefront/node_modules/vite/package.json'))('esbuild');
const output = await build({
  stdin: { contents: `export * from './storefront/src/config'; export * from './packages/theme-sdk/src/index'; export * from './storefront/src/api/read'; export { api } from './storefront/src/api/client';`, resolveDir: resolve(import.meta.dirname, '..'), loader: 'ts' },
  bundle: true, write: false, format: 'esm', platform: 'node', alias: { vue: require.resolve('../storefront/node_modules/vue/dist/vue.runtime.esm-bundler.js') }, define: { 'import.meta.env.SSR': 'false' },
});
const source = output.outputFiles[0].text;
const out = mkdtempSync(resolve(tmpdir(), 'zcard-bootstrap-test-'));
process.on('exit', () => rmSync(out,{recursive:true,force:true}));
const entry = (key, value) => ({ key, value_json: JSON.stringify(value) });
let serial = 0;
function fixture(preview = false) {
  const styles = new Map(), attrs = new Map();
  let runtime = { key: 'classic', theme_revision: 'builtin', config_revision: 'one', preview, values: { 'theme.primary_color': '#f8a245' }, capabilities: { cart: true }, public_config: { entries: [entry('theme.primary_color', '#f8a245'), entry('footer.about', '自定义页脚')] } };
  globalThis.document = {
    createElement: () => ({}),
    documentElement: { style: { setProperty: (k,v) => styles.set(k,v), removeProperty: k => styles.delete(k) }, setAttribute: (k,v) => attrs.set(k,v), removeAttribute: k => attrs.delete(k) },
    getElementById: id => id === 'zcard-theme-runtime' ? { textContent: JSON.stringify(runtime) } : null,
  };
  return { styles, attrs, load: () => { const file = resolve(out, `instance-${++serial}.mjs`); writeFileSync(file,source); return import(pathToFileURL(file)); } };
}
let calls = 0;
globalThis.fetch = async () => { calls++; throw new Error('unexpected request'); };
const f = fixture(), app = await f.load();
assert.equal(f.styles.get('--zc-primary'), '#f8a245');
assert.equal((await app.loadPublicConfig()).entries[1].value_json, '"自定义页脚"');
assert.equal(calls, 0, 'bootstrap must not wait for another request');
let release;
globalThis.fetch = async () => { calls++; await new Promise(r => release=r); return Response.json({ entries: [entry('theme.primary_color', '#112233'), entry('footer.about', '')] }); };
const pending1 = app.loadPublicConfig(true), pending2 = app.loadPublicConfig(true);
assert.equal(calls,1,'concurrent config requests must coalesce');
assert.equal((await app.loadPublicConfig()).entries[1].value_json,'"自定义页脚"','business refresh blocked the visual bootstrap');
release(); await Promise.all([pending1,pending2]);
assert.equal(f.styles.get('--zc-primary'),'#112233');
assert.equal(app.themeValue('theme.primary_color',''), '#112233', 'old HTML must not override a new publication');
assert.equal(app.publicConfig.value.entries[1].value_json,'""','explicit clearing must replace cached content');
const lastGood = app.publicConfig.value;
calls=0;
globalThis.fetch = async () => { calls++; return new Response('unavailable',{status:503}); };
await assert.rejects(app.loadPublicConfig(true));
assert.equal(calls,2,'bounded retry');
assert.equal(app.publicConfig.value,lastGood,'failure discarded good snapshot');
assert.equal(f.styles.get('--zc-primary'),'#112233');
globalThis.fetch = async () => Response.json({ error:'invalid config' });
await assert.rejects(app.loadPublicConfig(true));
assert.equal(app.publicConfig.value,lastGood);
globalThis.fetch = async () => Response.json({entries:[{key:'theme.primary_color',value_json:'broken-json'}]});
await assert.rejects(app.loadPublicConfig(true));
assert.equal(app.publicConfig.value,lastGood,'malformed value replaced saved appearance');
globalThis.fetch = async () => Response.json({entries:[entry('footer.about','')]});
await app.loadPublicConfig(true);
assert.equal(f.styles.has('--zc-primary'),false,'reset retained inline override');
assert.equal(app.themeValue('theme.primary_color','default'),'default','reset resurrected HTML value');
const preview = fixture(true), previewApp = await preview.load();
globalThis.fetch = async () => Response.json({ entries:[entry('theme.primary_color','#abcdef')] });
await previewApp.loadPublicConfig(true);
assert.equal(preview.styles.get('--zc-primary'),'#f8a245','preview lost priority');
const other = fixture(); const otherApp = await other.load();
assert.equal(otherApp.themeValue('theme.primary_color',''),'#f8a245','new document inherited previous configuration');
calls=0;
globalThis.fetch = async () => { calls++; return calls===1 ? new Response('',{status:503}) : Response.json({ok:true}); };
assert.deepEqual(await app.readJSON('/read'),{ok:true});
assert.equal(calls,2);
calls=0;
globalThis.fetch = async (_url, init) => { calls++; return new Promise((_resolve,reject) => init.signal.addEventListener('abort', () => reject(new Error('timeout')))); };
await assert.rejects(app.readJSON('/read',undefined,5));
assert.equal(calls,2,'timeout not bounded');
calls=0;
globalThis.fetch = async () => { calls++; return new Response('',{status:403}); };
await assert.rejects(app.readJSON('/read'));
assert.equal(calls,1,'permanent error was retried');
globalThis.localStorage = { getItem: () => 'test-member-token' };
let sentHeaders;
globalThis.fetch = async (_url, init) => { sentHeaders=init.headers; return Response.json({items:[],total:0}); };
await app.api.get('/products');
assert.equal(sentHeaders.Authorization,'Bearer test-member-token','catalog lost member pricing identity');
calls=0;
globalThis.fetch = async () => { calls++; return new Response('{"message":"failed"}',{status:503}); };
await app.api.post('/orders',{test:true});
assert.equal(calls,1,'order write must never inherit read retries');
const html = readFileSync(resolve(import.meta.dirname,'../storefront/dist/index.html'),'utf8');
assert(!html.includes('fetch failed') && !html.includes('暂无分类') && !html.includes('专业的自动发卡商城系统'),'build contains false customer state');
assert(html.includes('正在加载店铺') && html.includes('重新加载'),'missing bundle-failure recovery shell');
console.log('PASS: first paint seed, coalescing, publish/reset, failed/invalid reads, preview/site isolation, bounded retry/timeout, neutral production HTML');
