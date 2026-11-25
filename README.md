<p align="center">
  <img src=".github/logo.png" width="108" alt="ZCard" />
</p>

<h1 align="center">ZCard 2.0 · zcard-next</h1>

<p align="center">
  <strong>数字与实体商品商城 · 自动发卡与物流发货 · 双向上下游供货</strong><br />
  Go 重写，单二进制交付，内含商城与管理后台。
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.26.4+-00ADD8?logo=go" alt="Go 1.26.4+" />
  <img src="https://img.shields.io/badge/Kratos-v3-00B5AD" alt="Kratos v3" />
  <img src="https://img.shields.io/badge/Vue-3-42B883?logo=vue.js" alt="Vue 3" />
</p>

<p align="center">
  <a href="#快速开始">快速开始</a> ·
  <a href="doc/部署指南.md"><strong>部署指南</strong></a> ·
  <a href="#功能概览">功能概览</a> ·
  <a href="#常见问题">常见问题</a> ·
  <a href="#api-文档">API 文档</a> ·
  <a href="llms.txt">AI 文档索引</a> ·
  <a href="https://github.com/NovaWorks/zcard-next/releases">下载版本</a>
</p>

<p align="center">
  <sub>Self-hosted e-commerce in Go: digital goods, physical shipping, online services, and bidirectional supplier integration.</sub>
</p>

**ZCard 2.0（zcard-next）是用 Go 开发的开源自动发卡系统、自托管商城和双向供货平台，采用 MIT 许可证。** 一套系统管理卡密与数字商品、实体商品和在线服务商品，内置管理后台、商城、订单、支付与任务调度。

既可以销售自己的商品，也可以对接异次元 ACG-Faka、独角数卡 Dujiao Next、萌次元 mcy-shop 和其他 ZCard 站点，再通过供货 API 向下游销售。支持 SQLite、MySQL、PostgreSQL，中英文、多货币、三级分销与分站白标；各货源的适用商品和接口能力见下文。

前代版本：[ZCard 1.x](https://github.com/NovaWorks/ZCard)（PHP / Laravel）。本仓库 `zcard-next` 是它的 Go 重写版本。

## 核心优势

- **零售和供货在同一套系统中完成**：可以同时经营本地商品、从多个上游采购、向下游供货，结合本地加价、SKU 供货价与余额账本管理不同销售渠道。
- **覆盖多种交付方式**：数字商品支持卡密、固定内容、人工与上游交付；实体商品支持运费、包裹和收货流程；在线服务使用专用报价与进度页面。
- **从轻量部署起步**：官方发行版把商城和后台内嵌到 Go 二进制中，SQLite 可直接运行；业务需要时可选 MySQL、PostgreSQL 和 Redis，无需在服务器安装 PHP 或 Node.js 来运行发行版。
- **后台直接维护版本**：单二进制和官方受管理的单实例 Docker Compose 支持在线更新，具备发行验签、更新前备份、版本与健康检查，并提供回滚处理路径。
- **运营与资金功能配套**：钱包、积分、会员价、优惠券、分销佣金、分站定价与分账、退款和对账集中管理，减少另行拼接运营系统的工作。
- **源码和接口可查**：MIT 开源，提供 protobuf 与 OpenAPI 定义、部署文档和协议说明，便于自托管、二次开发及系统集成。

## 快速开始

> [!TIP]
> **更多安装步骤与配置说明，请参考 [ZCard 部署指南](doc/部署指南.md)。**
> 文档涵盖 Linux 一键安装、Docker、手动与面板部署、域名与 HTTPS，以及升级、备份和旧部署迁移。

| 安装方式 | 适用场景 | 准备事项 |
| --- | --- | --- |
| **Linux 一键安装** | 在服务器上直接运行，由 systemd 管理 | Bash、curl、Python 3.9+ |
| **Docker Compose** | 统一运行应用、MySQL 和 Redis | Docker、Compose v2.20+、Git、Bash、OpenSSL、curl、Python 3 |
| **手动 / 面板部署** | 已有宝塔、1Panel 或其他进程守护 | 对应架构的 Linux 二进制 |

### Linux 一键安装

```bash
curl -fsSL https://raw.githubusercontent.com/NovaWorks/zcard-next/main/scripts/zcard-install.sh -o /tmp/zcard-install.sh
sudo bash /tmp/zcard-install.sh install
```

脚本自动配置 systemd；Nginx 与 HTTPS 按部署指南另行配置。已有本地二进制时，可使用 `install --bin ./zcard-linux-amd64 --db sqlite`。

### Docker Compose

```bash
git clone https://github.com/NovaWorks/zcard-next.git
cd zcard-next
bash deploy/docker-install.sh
```

默认选择最新正式发行版；v1.2.95 起使用官方双架构镜像并接入在线更新助手，验证签名与发行文件校验信息，无需在服务器编译 Go 或前端。可通过 `ZCARD_VERSION` 指定已发布的版本；开发者需要源码构建时使用 `bash deploy/docker-install.sh --source`。受管理部署的升级请按[部署指南](doc/部署指南.md#日常管理与后台在线更新)操作。

启动后，浏览器打开 **`http://服务器IP:8000/install`** 完成初始化。Docker 安装向导中的连接信息：

| 配置项 | 填写内容 |
| --- | --- |
| 数据库类型 | MySQL |
| 数据库主机 / 端口 | `mysql` / `3306` |
| 数据库用户 / 库名 | `zcard` / `zcard` |
| 数据库密码 | `deploy/.env` 中的 `MYSQL_PASSWORD` |
| Redis 地址 | `redis:6379`，密码留空 |

<details>
<summary><strong>手动部署与 CLI 安装</strong></summary>

在部署目录执行：

```bash
./zcard serve -conf configs
```

无配置时自动生成 SQLite 引导配置与持久密钥，然后通过 `/install` 完成安装。若需要纯命令行初始化，可先执行 `./zcard install -conf configs`，再启动服务。

进程守护、运行目录与权限设置请参考 [部署指南](doc/部署指南.md)。

</details>

> [!IMPORTANT]
> 请备份配置、密钥和数据。**单二进制与官方受管理的单实例 Docker Compose 均支持后台在线更新**；其他 Docker 部署按各自方式拉取或重建镜像。受管理 Compose 的自动备份适用于应用卷内 SQLite 或默认配套 MySQL，外部数据库、自定义命令、多副本及 bind mount 数据目录不自动接管。程序回滚不等于恢复数据库，具体条件见[部署指南](doc/部署指南.md)。

## 功能概览

| 能力 | 主要功能 |
| --- | --- |
| **双向货源** | 多上游拿货、下游供货、加价与专属供货价、价格库存同步、后台导入任务 |
| **数字商品** | 多规格 SKU、加密卡密、固定内容、复用发货、人工及上游交付 |
| **实体商品** | 收货地址、配送地区、包邮／固定运费／到付、物流单号、分包发货、确认收货与退货入库 |
| **在线服务与接码** | 服务参数与动态报价、订单进度、短信号码与验证码；使用专用购买流程 |
| **商品管理** | 分类树、商品复制、商品排序批量保存、商品锁定、自定义下单表单与评价 |
| **订单与支付** | 游客及会员订单、购物车、多支付驱动、会员余额、退款与对账 |
| **会员与资金** | 会员等级、钱包、积分、优惠券、提现与退款 |
| **增长与运营** | 三级分销、分站白标、促销秒杀、营销抽奖、经营报表 |
| **内容与通知** | 多主题、中英文、多货币、SEO、工单、邮件短信及 Telegram 订单通知 |
| **安全与运维** | RBAC、两步验证、操作审计、卡密加密、数据备份、后台在线更新 |

## 功能详情

### 商品类型与交付方式

| 类型 | 适用商品与购买流程 |
| --- | --- |
| **卡密与数字商品** | 激活码、兑换码、下载链接等；本地卡密自动发货、固定内容直发、复用内容、人工发货或上游代发 |
| **实体商品** | 收集收货地址与配送地区，结算运费；后台填写承运商和物流单号，买家查看包裹并确认收货 |
| **在线服务商品** | 按上游服务规则填写链接、数量或评论等资料，确认动态报价后购买，在订单页查看处理进度 |
| **短信接码商品** | 通过兼容货源选择国家／地区、平台和报价，获取号码及短信验证码，查看取消或结束状态 |

普通商品支持游客或会员购买；在线服务与接码使用会员余额和专用购买流程，不适用普通购物车的全部规则。具体限制见下文。

### 双向上下游货源对接

**作为下游拿货**：

| 货源驱动 | 适用范围与前提 |
| --- | --- |
| **异次元 ACG-Faka**（`acg_faka`） | 分类、商品、规格、报价、库存及采购；二开站的字段和交付方式按已支持的兼容规则处理 |
| **独角数卡 Dujiao Next**（`dujiao_next`） | 商品导入、报价、库存同步与采购；以对方开放的供货接口为准 |
| **ZCard**（`zcard`） | ZCard 站点间对接，按上游声明的商品与接口能力工作 |
| **萌次元 mcy-shop**（`mcy_shop`） | 上游需启用 OpenApi 插件；支持多 SKU 与账号报价，详见[萌次元对接说明](docs/features/mcy-shop.md) |

- 多货源连接管理、连接测试、商品预览勾选导入、分类映射一键建目录
- 后台导入任务：显示进度与逐项结果，可停止后继续，重试未完成商品或单独补查库存；关闭页面后任务继续执行
- 全量 / 增量 / 定时同步（价格、库存、上下架，可设间隔与时间窗口防限流）
- 同步限流熔断与错误提示；不支持自动报价的商品可跳过，任务展示成功、跳过和失败统计
- 本地定价：比例加价、固定加价；封面采集本地化存储
- 付款后创建采购任务，记录采购单和失败详情；按协议能力处理幂等、查单与重试，结果不确定时保留原单待核对，避免重复采购
- 下单前按所选商品和规格实时校验上游库存；预检不代表已在上游预留库存

**作为上游供货**（开放 `/api/supply/*` API）：

- 下游账号申请 / 审核，api_key + api_secret
- 预存余额账本（充值、调账、余额快照、幂等流水）
- 商品级 / SKU 级专属供货价
- 幂等下单、未发货订单取消退款、回调通知
- 安全：HMAC-SHA256 四头签名、时间窗口、nonce 防重放、回调 SSRF 防护

**兼容协议供货**：为支持的异次元、独角数卡及萌次元货源客户端提供兼容入口，按对应协议创建下游账号并配置客户端即可对接。不同版本、二开站、插件以及商品类型存在能力差异，不把一个驱动的多 SKU、退款或回调能力视为所有协议通用；萌次元的双方插件要求与供货范围见[专项说明](docs/features/mcy-shop.md)。

### 商品与卡密

- 树形分类：拖拽调整层级与排序、分类图标、递归聚合子分类商品
- 商品列表直接修改排序，跨页保留修改后批量保存；支持商品复制、商品锁定与本地内容保护
- 多规格 SKU，按规格定价与库存；会员等级价；库存继承
- 按商品或 SKU 配置履约方式；可复用内容适合重复交付同一份说明、链接或资料，详见[分类与复用发货说明](docs/features/商品导入分类与复用发货.md)
- 商品自定义表单控件（下单时收集买家信息）、标签、会员商品组、批量上下架
- 商品评价：审核、虚拟评价、展示开关
- 卡密批量导入（预览、确认、批次撤销）、keyed-hash 去重、批量导出（超管专属并审计）
- AES-256-GCM 加密存储（不可关闭），查看完整卡密需二次确认并留审计
- 单卡禁用/启用、靓号识别、低库存提醒

### 实体商品与物流发货

- 商品设置可配送国家／地区、是否管理库存，支持实体 SKU 与实物库存管理
- 运费支持**包邮、固定运费、到付**；到付由快递向收件人收取，不再计入在线支付运费，订单保留成交时的运费条款
- 买家结算时填写收货地址，后台按订单商品项创建包裹并填写承运商、物流单号；同一订单可按不同商品项分包发货
- 发货前可更正地址，寄出后可更正物流信息并保留原因；支持买家确认收货、后台收货处理与退货入库
- 当前提供物流信息录入与包裹状态管理，不包含快递面单自动下单或物流轨迹实时查询；实体商品暂不支持积分兑换

### 在线服务与短信接码

- 在线服务按上游规则展示链接、数量、评论等字段，支持动态报价、下单资料快照与订单进度；是否可取消、补量，以及实际退款，以该服务能力和供货确认结果为准
- 短信接码通过兼容的 ZCard 供货端获取国家／地区、平台、报价、号码和验证码，订单页展示接码及结算状态
- 这两类商品采用会员余额专用购买流程。接码当前要求人民币基础币种、单件独立下单，不支持优惠券和积分；销售开关可用 `ZCARD_SMS_SALES_ENABLED=false` 暂停新接码订单
- 接口可用性和服务范围取决于实际货源账号及其开放能力

### 订单与支付

- 游客下单 / 会员下单 / API 下单；购物车合并结算
- 订单超时自动取消；联系方式收集策略可配
- 取货页：订单号 + 密码查询，卡密明文展示
- 多货币：基础货币记账（金额以「分」存储）、前台切换器实时换算、下单锁定汇率快照、支付回调金额双向核对

支付驱动（持续增加中）：

| 驱动 | 说明 |
|---|---|
| 支付宝 | 当面付 |
| 微信支付 | Native |
| 易支付 | epay 协议，兼容大量第三方聚合站 |
| 虎皮椒 Xunhupay | 微信／支付宝收款，按 APPID 分别配置渠道 |
| Stripe | 信用卡，多币种 |
| PayPal | REST API |
| EpuSdt | USDT 收款，按链选择渠道 |
| BEpusdt | 原生接口，CNY 计价，支持固定币种网络或多链收银台 |
| UPAY PRO | 原生接口，CNY 计价，支持 USDT、USDC 多链及 TRX |

- 一个支付渠道可承载多种支付方式，收银台按方式选择，支持自定义图标与描述
- 回调验签、幂等、金额核对；补单、退款（审计）；钱包余额支付

具体币种、支付方式和退款能力以各驱动及支付服务商配置为准，不是所有渠道都具备相同能力。

### 履约

- 本地卡密自动发货 / 固定内容直发（链接、兑换码）/ 人工发货 / 上游自动代发
- 支持复用内容、SKU 履约覆盖与实体物流发货；在线服务、接码使用各自的状态流程
- 付款后事件驱动交付，采购失败可追溯，并按错误类型与协议能力重试或转人工核对

### 钱包与资金

- 余额、积分双账本；充值满赠档位；礼品卡兑换
- 提现：申请、审核、打款，手续费支持固定金额或比例，收款方式白名单
- 手动调账（超管专属并审计）；积分抵现比例与上限可配

### 增长运营

- 三级分销佣金（按订单金额或利润计佣，支持退款逆向扣回）
- 会员等级：消费积分升降级、等级专属价
- 优惠券、秒杀、促销活动、首页推荐位、顶部自定义按钮
- 营销抽奖：活动、奖品、抽奖资格与中奖记录，见[抽奖功能说明](docs/features/lottery.md)
- 分站白标：分站主绑定自有域名（DNS TXT / HTTP 双验证）、独立定价、订单快照与利润分账账本

### 工作台与报表

- 经营 KPI 总览、趋势图、热销排行、佣金列表
- 对账系统：对账任务创建与执行、逐笔明细核对
- 日结统计、流量统计（PV / UV、访问明细）

### 客服与触达

- 工单系统（前台提交、内部备注、解决/关闭流转）
- 邮件（SMTP）与短信（阿里云、腾讯云、七牛）模板通知、群发预估与取消
- Telegram 订单通知：新订单、付款成功、发货完成、退款成功；可配置接收目标与话题，查看投递记录并补发

### 内容与前台

- 横幅、文章栏目、站点公告（文本/图片/轮播）
- 多模板体系（PC 与移动端独立模板，主题市场式安装）
- SEO：robots 与 sitemap 动态生成、SSG 预渲染、爬虫动态渲染，内容修改实时生效
- 多语言（中/英）、多币种切换；移动端全面适配

### 安全与权限

- RBAC 细粒度权限点（100+），敏感操作限超管（卡密明文、导出、资金操作）
- 操作日志、安全日志、访问统计三线审计
- 管理员 Google Authenticator 两步验证：扫码确认绑定、一次性恢复码、换绑与命令行强制恢复（见[部署指南](doc/部署指南.md#后台两步验证与恢复)）
- 登录图形验证码、单 IP 待付款订单限流、风控黑名单
- 生产密钥环境变量注入，卡密加密密钥轮换工具（reencrypt-cards）

### 部署与在线更新

- 单二进制交付（管理后台 + 商城 + SQLite 驱动内嵌），双架构 amd64 / arm64
- 多种安装方式：一键脚本（systemd）、Docker Compose、浏览器向导、命令行
- **后台一键在线更新**：单二进制支持 GitHub 直连、大陆加速镜像与自建静态源，提供 ED25519 验签、更新前数据库备份与版本历史
- **Docker 后台更新**：官方受管理的单实例 Compose 由更新助手验签、拉取固定 digest、停机备份、重建应用并检查镜像、版本与数据库健康；关闭浏览器不会取消已提交任务
- **回滚边界**：按程序启动状态与数据库迁移兼容性决定回滚路径，不承诺所有失败都能自动回滚，也不自动用旧数据库覆盖新订单；部署条件、备份工具与手动恢复步骤见[部署指南](doc/部署指南.md)
- 后台任务调度内置（周期任务随服务启动，无需额外 cron）
- CLI 运维命令：install、serve、migrate、admin、self-update、dbtest、reencrypt-cards

## API 文档

全部接口（管理后台、前台、供货 API）由 protobuf 自动生成 OpenAPI 规范：[server/api/openapi.yaml](server/api/openapi.yaml)（随代码同步生成）。

<details>
<summary><strong>供货 API 端点与鉴权说明</strong></summary>

| 端点 | 说明 |
|---|---|
| `GET /api/supply/ping` | 连接测试（免签名） |
| `POST /api/supply/categories` | 商品分类 |
| `GET /api/supply/products` | 商品列表 |
| `GET /api/supply/products/{id}` | 商品详情 |
| `GET /api/supply/products/{id}/stock` | 实时库存 |
| `POST /api/supply/orders` | 幂等下单 |
| `GET /api/supply/orders` | 时间窗内订单列表与对账 |
| `GET /api/supply/orders/{id}` | 订单查询 |
| `POST /api/supply/orders/{id}/cancel` | 取消退款 |
| `POST /api/supply/orders/{id}/refund` | 申请退款 |

以上为原生 ZCard Supply 协议。除免签名的连通性探测外，请求使用 HMAC-SHA256 签名（api_key + api_secret），携带 `X-Supply-Key`、`X-Supply-Timestamp`、`X-Supply-Nonce`、`X-Supply-Signature` 四个请求头，通过时间窗口与 nonce 防重放；具体算法和字段见 [Supply protobuf](server/api/supply/v1/supply.proto) 及 OpenAPI。兼容其他系统的协议入口按各自鉴权规则配置。

</details>

## 为什么开发 2.0

2.0 将商城、后台和内置任务调度集中到单个 Go 程序中，支持多种数据库，简化安装与运维，同时扩展实体商品、在线服务和双向供货等业务场景。

<details>
<summary><strong>从 1.x 到 2.0：设计取舍与版本对比</strong></summary>

我们的 v1 版本基于 PHP 8.3 + Laravel，功能完整，也仍在维护。但在长期运营中，一些架构层面的问题反复出现：

- 部署环节多：PHP 版本与扩展、Composer、PHP-FPM、Supervisor、cron 都要正确配置，任何一环出问题都会导致服务异常，排查成本高；
- 后台队列是硬依赖：队列进程一旦没有运行，用户付款后无法拿到卡密，这类故障在低配服务器上并不少见；
- 升级麻烦：在线更新后需要手动重启 PHP-FPM 和 Supervisor，出问题回退旧版本全靠手工；
- 必须 MySQL：轻量场景下也希望用 SQLite 直接跑；
- 分站与分销功能互斥，无法同时使用。

2.0 用 Golang 重写，就是为了解决这些问题：

| 对比项 | ZCard 1.x | ZCard 2.0 |
|---|---|---|
| 部署 | PHP + Composer + PHP-FPM + Supervisor + cron | 单个二进制文件（前端内嵌） |
| 后台任务 | 队列强依赖 | Redis 可选，缺失时自动降级为同步执行 |
| 升级 | 手动重启服务，回退靠手工 | 后台在线更新、验签与备份，按兼容性处理程序回滚 |
| 数据库 | MySQL | SQLite / MySQL / PostgreSQL |
| 分站与分销 | 互斥 | 可同时使用 |

选择 Golang 的原因：

- 编译为单个静态二进制，配合 `go:embed` 把前端打包进去，交叉编译一次就得到完整的安装包，部署就是复制一个文件；
- goroutine 并发模型适合上游同步、事件分发、队列消费这类异步工作，不需要常驻多个进程；
- 小型站点可以从单二进制与 SQLite 起步，再按商品规模、同步量和访问量选择数据库与服务器配置；
- 静态类型加上架构守护测试（依赖方向、SQL 收口等由 CI 强制），项目长期迭代不容易腐化。

</details>

## 常见问题

### ZCard 只能卖卡密吗？

可以经营卡密、数字内容、人工交付商品、实体商品和兼容上游的在线服务。实体商品有收货地址、运费与包裹流程；在线服务和短信接码有独立购买及进度页面。

### ZCard 支持一键在线更新吗？

支持。单二进制和官方受管理的单实例 Docker Compose 可在后台在线更新。其他 Docker 部署、外部数据库与自定义部署需要按[部署指南](doc/部署指南.md)确认更新和备份方式；更新可能短暂停机，回滚程序不等于回滚数据库。

### ZCard 能同时作为货源下游和供货上游吗？

可以。同一站点可从多个上游导入和采购商品，也能创建下游账号、配置供货价并通过 API 供货。各协议支持的商品、规格、查单、取消和退款范围不同，接入前应核对相应驱动说明。

### 支持哪些数据库、语言和货币？

支持 SQLite、MySQL、PostgreSQL；前台提供简体中文和英文。基础货币、展示货币与成交汇率快照用于记账及结算，多币种支付仍取决于支付驱动，接码当前仅支持 CNY 基础币种。

### 如何查找接口和具体功能说明？

先查看下方[文档导航](#文档导航)，接口契约以 [OpenAPI](server/api/openapi.yaml) 和 protobuf 定义为准。提供给 AI 助手或检索工具时，可以从纯文本 [llms.txt](llms.txt) 开始，再读取对应 Markdown 文档；具体能力以使用的代码或发行版本为准。

## English overview

**ZCard 2.0 (`zcard-next`) is an MIT-licensed, self-hosted e-commerce platform written in Go.** It supports digital goods and automated card/code delivery, physical products with shipping workflows, online service orders, and bidirectional supplier integration. The release binary embeds the Vue storefront and admin panel. SQLite, MySQL, and PostgreSQL are supported, with Chinese/English storefronts, multiple currencies, wallets, affiliate commissions, and white-label substores.

Supplier adapters include ZCard, ACG-Faka, Dujiao Next, and mcy-shop (OpenApi plugin required). In-admin online updates are available for native binaries and official managed single-instance Docker Compose installations. Provider capabilities, service purchases, database backups, and rollback each have deployment or product-specific requirements described above and in the [deployment guide](doc/部署指南.md). Start with the [AI-readable documentation index](llms.txt) for focused references.

## 文档导航

| 文档 | 内容 |
| --- | --- |
| [部署指南](doc/部署指南.md) | 安装、数据库、HTTPS、后台在线更新、备份与恢复 |
| [OpenAPI](server/api/openapi.yaml) | 管理后台、商城、供货接口契约 |
| [萌次元对接](docs/features/mcy-shop.md) | OpenApi 插件、双方对接方法、多 SKU 与供货范围 |
| [商品导入、分类与复用发货](docs/features/商品导入分类与复用发货.md) | 导入分类、内容复用与履约配置 |
| [ACG 导入兼容](docs/features/acg-legacy-import-compatibility.md) | 二开商品字段、规格与库存兼容规则 |
| [营销抽奖](docs/features/lottery.md) | 抽奖活动、奖品与参与规则 |
| [主题开发](docs/themes/README.md) | 主题包、配置与接口集成 |
| [llms.txt](llms.txt) | 面向 AI 助手的项目摘要与纯文本引用入口 |
| [正式发行与更新说明](https://github.com/NovaWorks/zcard-next/releases) | 可部署发行版、附件与版本变更 |

## 开发与架构

### 技术栈

| 层 | 选型 |
|---|---|
| 后端 | Go、Kratos v3、Ent ORM、Atlas 版本化迁移、wire 依赖注入、protobuf 接口定义 |
| 管理后台 | Vue 3、Naive UI、UnoCSS（soybean-admin 定制） |
| 商城前台 | Vue 3、Tailwind CSS v4、vite-ssg 预渲染 |
| 存储 | SQLite（纯 Go 驱动）/ MySQL 8 / PostgreSQL 15+；Redis 可选 |
| 部署 | 单二进制、Docker、systemd 一键脚本、宝塔进程守护 |

### 本地开发

```bash
cd server
make init
make generate
make test
make run
```

<details>
<summary><strong>项目目录结构</strong></summary>

```
zcard-next/
├── server/                  # Go 后端
│   ├── api/                 # protobuf 接口定义 + OpenAPI 生成物
│   │   ├── admin/v1/        # 管理后台 API
│   │   ├── storefront/v1/   # 商城前台 API
│   │   └── supply/v1/       # 对外供货 API（HMAC 签名）
│   ├── cmd/zcard/           # 程序入口与子命令
│   ├── internal/
│   │   ├── conf/            # 配置定义
│   │   ├── server/          # HTTP/gRPC 服务与中间件
│   │   ├── bootstrap/       # 模块装配
│   │   ├── mods/            # 业务模块（订单、支付、货源、分销等）
│   │   ├── data/            # 数据层：ent schema、迁移、事务
│   │   └── platform/        # 基础设施（金额、加密、队列、事件、租户）
│   ├── migrations/          # 三方言迁移文件
│   └── Makefile             # 生成 / 测试 / 构建一键化
├── admin/                   # 管理后台前端
├── storefront/              # 商城前台前端
├── deploy/                  # Dockerfile、docker-compose、安装脚本
├── scripts/                 # 一键安装脚本
├── doc/                     # 部署指南
├── docs/features/           # 功能与协议说明
└── llms.txt                 # AI 文档索引
```

</details>

<details>
<summary><strong>工程纪律</strong></summary>

- 金额统一使用 int64 分，禁止浮点
- 卡密强制加密存储，永不落明文
- 模块间只通过窄接口与事件通信，由架构测试强制
- 对外单号使用雪花 ID，与内部自增主键严格分离
- 三方言迁移独立版本化，启动自动迁移，失败拒绝启动

</details>

## 许可证与商业使用

本项目采用 **[MIT License](LICENSE)**，允许商用、收费服务、二次开发和销售软件副本，无需另行取得本项目的商业授权；MIT 不要求公开修改后的源码。

复制或分发软件副本或实质性部分（包括修改版）时，必须保留完整的版权声明和许可声明。**本项目版权声明包含 ZCard 名称及[原仓库地址](https://github.com/NovaWorks/zcard-next)，请完整保留原始出处。** 可以增加自己的修改说明和版权声明，但不能替换原声明。

第三方组件继续适用其各自的许可证，包括后台框架的 [Soybean 版权声明](admin/LICENSE)。项目出处见 [NOTICE](NOTICE)，详细中文说明见 [商业使用与出处保留说明](LICENSE.zh-CN.txt)；正式条款以英文 MIT 许可证为准。

## 更新与交流

- **版本发布**：[GitHub Releases](https://github.com/NovaWorks/zcard-next/releases) 提供可部署版本与更新说明。
- **升级指引**：单二进制与官方受管理的单实例 Docker Compose 支持后台在线更新；其他 Docker 部署按各自方式升级。操作步骤、备份条件与回滚边界见[部署指南](doc/部署指南.md)。
- **问题反馈**：欢迎通过 [Issues](https://github.com/NovaWorks/zcard-next/issues) 报告问题、提出功能建议，或提交 PR。

| 交流讨论 | 更新通知 |
| --- | --- |
| [Telegram 群组 · @ZhonCard](https://t.me/ZhonCard) | [Telegram 频道 · @ZCardGroup](https://t.me/ZCardGroup) |
