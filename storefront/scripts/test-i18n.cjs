// Runs the real locale module with isolated browser storage and no network.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('../node_modules/typescript');
const vue = require('../node_modules/vue');
const sfc = require('../node_modules/vue/compiler-sfc');
const root = path.resolve(__dirname, '..');
const english = require('../src/locales/en.json');
const storage = new Map();
let storageBlocked = false;
const document = { documentElement: { lang: '' } };
const source = fs.readFileSync(path.join(root, 'src/i18n.ts'), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS, esModuleInterop: true } }).outputText;
const moduleExports = {};
vm.runInNewContext(code, { exports: moduleExports, require: id => id === 'vue' ? vue : english, document, window: {}, localStorage: { getItem: key => { if (storageBlocked) throw new Error('Storage unavailable'); return storage.get(key) ?? null; }, setItem: (key, value) => { if (storageBlocked) throw new Error('Storage unavailable'); storage.set(key, value); }, removeItem: key => storage.delete(key) } });
const { applyLocaleConfig, selectLocale, locale, enabledLocales, t, uiText, apiError, languageHeaders, normalizeLocale } = moduleExports;
const config = (defaultLocale, enabled) => [ { key: 'i18n.default_locale', value_json: JSON.stringify(defaultLocale) }, { key: 'i18n.enabled_locales', value_json: JSON.stringify(enabled) } ];
applyLocaleConfig(config('en', ['zh_CN', 'en']));
assert.equal(locale.value, 'en');
assert.equal(document.documentElement.lang, 'en');
assert.equal(languageHeaders()['Accept-Language'], 'en');
assert.equal(t('登录'), 'Sign in');
assert.equal(normalizeLocale('en_US'), 'en');
assert.equal(normalizeLocale('en-US'), 'en');
assert.equal(normalizeLocale('zh-CN'), 'zh_CN');
assert.equal(normalizeLocale('de'), null);
let rendered;
const stop = vue.watchEffect(() => { rendered = t('登录'); }, { flush: 'sync' });
assert.equal(selectLocale('zh_CN'), true);
assert.equal(rendered, '登录');
assert.equal(document.documentElement.lang, 'zh-CN');
applyLocaleConfig(config('en', ['zh_CN', 'en']));
assert.equal(locale.value, 'zh_CN', 'enabled customer preference overrides the site default');
applyLocaleConfig(config('en', ['en']));
assert.equal(locale.value, 'en', 'disabled preference falls back to the site default');
assert.equal(storage.has('zcard_locale'), false);
assert.equal(enabledLocales.value.length, 1);
assert.equal(selectLocale('zh_CN'), false);
applyLocaleConfig(config('de', ['unsupported']));
assert.equal(locale.value, 'zh_CN', 'malformed configuration has a supported fallback');
applyLocaleConfig(config('en', ['zh_CN', 'en']));
assert.equal(uiText('请填写「Email」'), t('请填写「{0}」', ['Email']));
assert.equal(apiError({ reason: 'catalog.STOCK_INSUFFICIENT', message: '库存不足' }, 400), t('库存不足，请调整数量后重试'));
assert.equal(apiError({ reason: 'identity.LOGIN_FAILED', message: '账号或密码错误' }, 401), t('账号或密码错误'));
assert.equal(apiError({ reason: 'wallet.CURRENCY_MISMATCH', message: '旧服务的内部错误' }, 400), t('账户币种与基础货币不一致，请联系管理员迁移'));
assert.equal(apiError({ reason: 'unknown.ERROR', message: '数据库内部故障' }, 500), t('服务暂时不可用，请稍后重试'));
assert.equal(apiError({ message: 'Order has expired' }, 400), 'Order has expired');
storageBlocked = true;
selectLocale('zh_CN');
applyLocaleConfig(config('en', ['zh_CN', 'en']));
assert.equal(locale.value, 'zh_CN', 'an active customer choice survives a refresh when storage is unavailable');
applyLocaleConfig(config('en', ['en']));
assert.equal(locale.value, 'en', 'disabled customer choices fall back even when storage is unavailable');
storageBlocked = false;
stop();
// Every translated source phrase and every interpolation has an English counterpart.
function files(dir) { return fs.readdirSync(dir, { withFileTypes: true }).flatMap(entry => entry.isDirectory() ? files(path.join(dir, entry.name)) : [path.join(dir, entry.name)]); }
let keys = new Set();
for (const file of files(path.join(root, 'src')).filter(file => /\.(ts|vue)$/.test(file))) {
  const source = fs.readFileSync(file, 'utf8');
  if (file.endsWith('.vue')) {
    const parsed = sfc.parse(source, { filename: file });
    assert.deepEqual(parsed.errors, [], `${file}: invalid Vue component`);
    assert(parsed.descriptor.template, `${file}: Vue template is missing`);
  }
  for (const match of source.matchAll(/(?:\$t|\bt)\(\s*((?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'))/g)) {
    const literal = ts.createSourceFile('phrase.ts', `const phrase = ${match[1]}`, ts.ScriptTarget.Latest, true).statements[0].declarationList.declarations[0].initializer;
    const key = literal.text.replaceAll('&quot;', '"').trim().replace(/\s+/g, ' ');
    if (!/[\p{Script=Han}]/u.test(key)) continue;
    keys.add(key);
    assert.equal(typeof english[key], 'string', `${file}: missing English translation for ${key}`);
    assert.equal(/[\p{Script=Han}]/u.test(english[key]), false, `Untranslated English entry: ${key}`);
    assert.deepEqual([...new Set(key.match(/\{\d+\}/g) || [])].sort(), [...new Set(english[key].match(/\{\d+\}/g) || [])].sort(), `Interpolation mismatch: ${key}`);
  }
}
console.log(`Locale selection, disabled preferences, reactive labels, error reasons, cached feedback and ${keys.size} translation keys passed.`);
