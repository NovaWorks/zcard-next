# 商品会员购买资格插件

第一方可信插件；宿主需要完成 P2，core >= 1.2.89、plugin API 1。`evaluate(input)` 必须同步返回 `{allow:boolean, reason:string}`；ID/版本计数为字符串，会员资格按所选有效等级 ID 精确匹配。样例不修改价格、库存或会员。

脚本直接为兼容 goja 的 JavaScript，无 Node.js、模块导入或异步依赖。若采用 TypeScript，先构建为无 import/export 的单文件脚本并保留全局 `evaluate`；发布物仍只有 `manifest.json` 和 `main.js`。升级同时修改 manifest 的三段版本号；配置 schema v1 保持兼容。

## 本地开发签名

从仓库根运行。仅用于开发的密钥放在临时目录，禁止将生产私钥提交到源码或放入插件包：

```sh
plugin_demo=$(mktemp -d)
openssl genpkey -algorithm ED25519 -out "$plugin_demo/key.pem"
chmod 600 "$plugin_demo/key.pem"
openssl pkey -in "$plugin_demo/key.pem" -pubout -outform DER \
  | tail -c 32 | openssl base64 -A > "$plugin_demo/public-key.base64"
go -C server run ./tools/plugin-pack \
  --manifest ../examples/plugins/member-purchase-gate/manifest.json \
  --script ../examples/plugins/member-purchase-gate/main.js \
  --key "$plugin_demo/key.pem" --key-id dev-local \
  --out "$plugin_demo/package"
```

将 `public-key.base64` 的公钥加入**隔离测试实例**的 `data.plugin_trusted_keys.dev-local`。该启动配置变更需要重新启动实例；配置完成后的包导入/启用/升级不需要重启。`--out` 必须是尚不存在的目录；工具生成 descriptor、原始签名和 `.zplug`，标准输出是 archive digest。生产应使用自己的签名与信任管理流程。

CLI/API 操作、资源限制和恢复方式见 [P2操作说明](../../../docs/plans/contracts/p2-runtime.md)。不要直接编辑已安装工件；宿主重新验签，不从可变解包文件执行脚本。
