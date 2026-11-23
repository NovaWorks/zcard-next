# ZCard 货源的规格与购买资料

开源版新增 ZCard 连接时仍使用供货账户的 Key/Secret。多规格和额外购买资料需要供货端同步支持 `card_purchase.v1`；两端更新后重新同步商品。旧供货端继续提供它原有的商品与服务能力。

## 商品和采购

新版客户端在目录、详情及库存查询中声明 `card_purchase.v1`，接收供货端的规格、各规格价格和库存，以及 `purchase_form.fields` 填写规则。停用规格不导入。商品刷新保留规格和表单合同，实际下单按选定规格计价。

```json
{
  "product_id": "101",
  "sku_id": "201",
  "quantity": 2,
  "downstream_order_no": "retail-order-1",
  "required_capability": "card_purchase.v1",
  "manual_form_data": {"f_opaque_301": "customer-value"}
}
```

规格和字段标识来自供货端，不填真实货源的商品或规格编号。数字字段传 JSON number，多选传字符串数组，其他资料传字符串。采购使用下单时保存的规格与资料；供货端明确拒绝无效字段时转入相应错误处理。

库存查询可不带规格来刷新汇总库存。实际购买数量检查使用选定规格：

```http
GET /api/supply/products/101/availability?quantity=2&sku_id=201&capabilities=card_purchase.v1
```

该接口沿用供货 HMAC，只读且不采购。未知库存保留未知数值，按实际购买数量确认；结果不明确时继续核对原订单，避免再次采购。

## 验证

客户端合同回归：

```sh
cd server
GOTOOLCHAIN=go1.26.4 go test ./internal/mods/supply/adapter -run TestZCard
```

还使用独立的 Market、开源版程序和数据库配合模拟上游验证原生规格及购买资料经过 ZCard 的完整购买链路。两站商品和规格编号故意错开，检查选价、付款、上游实际规格、必填资料、交付回执及单次扣款。供货端配套实现与其私有适配器不包含在开源版发行中。
