# 萌次元 mcy-shop 上下游对接

渠道驱动、下游账号协议均为 `mcy_shop`。上游和下游的配置入口与独角数卡、异次元一致。

## 从萌次元采购（萌次元 → ZCard）

1. 上游萌次元站需安装、启用 **OpenApi** 插件，并提供对接 ID、对接密钥。
2. ZCard 后台「渠道」新增连接，类型选「萌次元 mcy-shop」。
3. 地址填写上游网站根地址，例如 `https://supplier.example.com`，不要追加 `/plugin/open-api`。
4. 填写 `app_id`、`app_key`，测试连接，再预览、导入商品。按上游实际币种配置汇率；该协议不返回币种代码。

支持分类、商品介绍与图片、多 SKU、账号报价、实时库存预检、同步下单和交付。购买控件支持可转换为本站表单的标量字段；多选数组控件暂不支持。正式导入与价格同步通过 `/amount` 逐 SKU 读取账号单件报价，避免把参考价当采购成本。

前台商品总库存通过 `sku/stock` 逐规格查询后合计，详情页各规格独立读取并缓存库存，展示查询合并并发请求、复用 15 秒内的查询结果。缺货为 `0`，读取失败或非数字库存（如“大量”）为未知，不使用其他规格或商品总量替代。下单前仍按所选规格实时预检。部署此修复需要更新 ZCard 服务端；仅更新主题不能修复库存接口。

已核实的接口均为 POST 表单，使用 `Api-Id`、`Api-Signature` 请求头：

| 路径前缀 `/plugin/open-api/` | 用途 |
| --- | --- |
| `connect` | 账号与余额 |
| `items`、`item` | 可见目录、单品及 SKU |
| `sku/stock`、`sku/state` | 实时库存、指定数量库存检查 |
| `amount` | 指定 SKU 和数量报价 |
| `trade` | `sku_id`、`quantity`、`trade_no` 和购买资料；返回 `contents` |

`trade_no` 为本地采购幂等键 MD5 的前 24 位，与现有异次元萌次元客户端一致。**不能据此假定所有上游插件都支持幂等重放。** 下单请求不自动重试；超时、断连、响应异常、发货占位文案或数量不符统一转人工核对原单，避免重复采购或已扣款后自动退款。

目前核实的客户端契约没有独立查单、自动退款、回调和订单列表接口，这些能力返回不支持。目录仅代表可见商品，不用于确认上游永久删除。文字库存（例如“大量”）记为未知，不作为无限库存放行。

## 向萌次元供货（ZCard → 萌次元）

1. ZCard 后台「下游账号」创建「萌次元 mcy-shop 兼容」账号，审核通过、充值并设置供货价；前台个人中心也可申请此协议。
2. 萌次元站安装、启用其 **异次元 V3 / MCYShopV3** 货源插件。
3. 在该插件的货源配置中填写 ZCard 根地址，商户 ID 填 ZCard 的 `api_key`，密钥填 `api_secret`，再拉取商品。

该方式按官网货源插件示例兼容 `/shared/commodity/items`、`inventory`、`inventoryState`、`trade` 等接口。无需把萌次元后台的用户登录接口当作外部 API。

本站同时提供上表列出的 OpenApi 兼容接口，供已有萌次元 OpenApi 客户端直接连接。仅允许审核通过的 `mcy_shop` 账号，执行账号 IP 白名单、供货定价、余额与订单幂等校验。

下游供货范围沿用现有供货核心：**人民币、无本地 SKU 的本地卡密商品**；OpenApi 输出一个商品级虚拟 SKU（ID 等于商品 ID）。多规格商品、上游代发商品及人工发货商品不在本次对外供货范围。上游导入多 SKU 与对外供货是两个独立能力。

## 升级与验证

- MySQL 的账号协议列使用 ENUM，随正常启动迁移加入 `mcy_shop`；迁移由 Atlas 从 Ent schema 生成。
- SQLite、PostgreSQL 的该列是文本，不需要新增数据库 DDL。
- 本地契约测试覆盖 PHP 签名向量、双方真实 HTTP 适配互通、SKU 选择、账号报价、库存、重复提交、鉴权与不确定采购结果。
- 已完成本地测试和构建；尚未连接实际运营中的萌次元站或安装其 OpenApi 插件实测。

## 协议依据

- [萌次元官网：货源插件开发说明](https://wiki.mcy.im/#/zh-cn/developer/plugin/started)：`Ship`、`ForeignShip` 与异次元 V3 插件示例。已通过浏览器核对。
- `mcy-shop/kernel/Util/Str.php`：PHP `generateSignature` 算法，包含过滤空字符串与数组、排序、`http_build_query`、`urldecode` 和 MD5。
- 现有 `acg-faka/app/Service/Bind/Shared.php` 的 `mcyRequest`、`type=1` 分支：OpenApi 请求头、端点、商品 SKU 字段、询价及下单返回值。OpenApi 插件实现未包含在所提供的 mcy-shop 主仓库中。
