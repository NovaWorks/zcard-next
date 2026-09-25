// Run after installing admin dependencies: node scripts/test_money.cjs
// Execute the real TS modules; only network/storage dependencies are stubbed.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('../admin/node_modules/typescript');
const root = path.resolve(__dirname, '..');
let currencies = [{ code: 'CNY', symbol: '¥', position: 'prefix', precision: 4, rate_json: '1', enabled: true }];
function load(file, overrides = {}) {
  const source = fs.readFileSync(path.join(root, file), 'utf8').replaceAll('import.meta.env', '({SSR:false})');
  const output = ts.transpileModule(source, {compilerOptions:{target:ts.ScriptTarget.ES2020,module:ts.ModuleKind.CommonJS}}).outputText;
  const exports = {};
  const requireMock = (id) => {
    if (id.includes('packages/money')) return shared;
    if (id === 'vue') return require('../admin/node_modules/vue');
    if (id.includes('theme-sdk')) return { mergeThemeConfig: x => x };
    if (id === '../config') return { loadPublicConfig: async () => ({ entries: [] }) };
    if (id === './read') return { readJSON: async () => { throw new Error('unexpected network call'); } };
    if (id === '@/utils/storage') return {localStg:{get:()=> 'test-token'}};
    if (id === '@/service/api') return {
      fetchSettings: async()=>({data:{items:[]}}),
      fetchCurrencies: async()=>({data:{currencies}}),
    };
    throw new Error(`unexpected dependency ${id}`);
  };
  vm.runInNewContext(output, {exports,require:requireMock,localStorage:{getItem:()=>null},...overrides}, {filename:file});
  return exports;
}
const shared = load('packages/money/index.ts');
const admin = load('admin/src/utils/money.ts');
const shop = load('storefront/src/api/client.ts');
async function main() {
  for (const api of [admin,shop]) {
    assert.equal(api.formatMoney(200),'¥2.00');
    for(const prec of [0,1,2,3,4,5,6,7,8]) {
      api.setCurrency({precision:prec});
      const text=prec ? `2.${'0'.repeat(Math.min(prec,2))}`:'2';
      assert.equal(api.formatMoney(200),`¥${text}`);
      assert.equal(api.fenToYuan(200),text);
      assert.equal(api.centsToYuan(200),2);
      assert.equal(api.yuanToFen(2),200);
      assert.equal(api.yuanToFen(api.centsToYuan(200)),200);
      assert.equal(api.yuanToFen(-2),-200); // legitimate signed balance adjustments
      assert.equal(api.yuanToFen(0.29),29);
      assert.equal(api.yuanToFen(2.0000),200);
      for(const invalid of [0.001,2.0001,Infinity,NaN,1e20]) assert.throws(()=>api.yuanToFen(invalid));
      for(const cents of [0,1,29,199,200,201,-199,1000000000]) assert.equal(api.yuanToFen(api.centsToYuan(cents)),cents);
    }
    api.setCurrency({precision:4,position:'suffix',symbol:'元'});
    assert.equal(api.formatSignedMoney(-200),'-2.00元');
    assert.equal(api.formatSignedMoney(200),'+2.00元');
    api.setCurrency({precision:NaN});
    assert.equal(api.formatMoney(200),'¥2.00');
  }
  for(const rate of ['1','1.0','1.00000000']) {
    shop.setCurrency({precision:4,rate});
    assert.equal(shop.formatMoney(200),'¥2.00');
  }
  shop.setCurrency({precision:4,rate:'0.14',symbol:'$'});
  assert.equal(shop.formatMoney(200),'$0.28');
  assert.equal(shop.yuanToFen(2),200); // forms remain base currency
  assert.equal(shared.formatCents(199,1),'2.0');
  assert.equal(shared.formatCents(100,2,'1.005'),'1.01');
  assert.equal(shared.formatCents(-100,2,'1.005'),'-1.01');
  assert.equal(shared.formatCents(-1,0),'0');
  // Trailing-zero removal must preserve significant digits and exact rounding.
  const cases = [
    [0, 6, '1', '0.00'], [210, 6, '1', '2.10'], [201, 6, '1', '2.01'],
    [100, 6, '2.123400', '2.1234'], [100, 6, '2.100001', '2.100001'],
    [100, 6, '0.000001', '0.000001'], [100, 8, '0.00000001', '0.00000001'],
    [100, 4, '2.12345', '2.1235'], [100, 6, '9.9999995', '10.00'],
    [100, 6, '0.0000005', '0.000001'], [-100, 6, '0.0000004', '0.00'],
    [-100, 6, '2.123400', '-2.1234'], [100, 1, '2.15', '2.2'],
    [100, 0, '2.5', '3'], [100, 2, '2.100001', '2.10'],
  ];
  for (const [cents, precision, rate, expected] of cases) {
    assert.equal(shared.formatCents(cents, precision, rate), expected);
    shop.setCurrency({precision,rate,symbol:'$'});
    assert.equal(shop.formatMoney(cents), '$'+expected);
    shop.setCurrency({precision,rate,symbol:'元',position:'suffix'});
    assert.equal(shop.formatMoney(cents), expected+'元');
  }
  await admin.initCurrency(true);
  assert.equal(admin.formatMoney(200),'¥2.00');
  assert.equal(admin.centsToYuan(200),2);
  delete currencies[0].precision; // proto3 omission means explicit zero
  await admin.initCurrency(true);
  assert.equal(admin.formatMoney(200),'¥2');
  shop.api.get=async p=>({data:p==='/currencies'?{currencies}:{entries:[]}});
  await shop.initCurrency();
  assert.equal(shop.getCurrency().precision,0);
  assert.equal(shop.formatMoney(200),'¥2');
  assert.equal(shop.yuanToFen(2),200);
  console.log('PASS: actual admin/storefront money helpers, precision 0–8, exchange display, signed inputs, cent validation and config loading');
}
main().catch(e=>{console.error(e);process.exitCode=1});
