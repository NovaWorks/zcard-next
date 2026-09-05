<p align="center">
  <img src="storefront/src/assets/logo.png" width="120" alt="ZCard" />
</p>

# ZCard 2.0（zcard-next）

双向上下游自动发卡 / 数字商品销售系统。既能对接异次元、独角数卡、其他 ZCard 站点自动拿货，也能开放供货 API 向下游系统供货。

`Go · Kratos v3 · Vue 3 · 双向货源 · 自动发卡 · 多货币 · 多语言 · 三级分销 · 分站白标 · 单二进制`

![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)
![Kratos](https://img.shields.io/badge/Kratos-v3-00B5AD)
![Vue](https://img.shields.io/badge/Vue-3-42B883?logo=vue.js)
![SQLite](https://img.shields.io/badge/SQLite-内嵌-003B57?logo=sqlite)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-4169E1?logo=postgresql)
![MySQL](https://img.shields.io/badge/MySQL-8.0+-4479A1?logo=mysql)

Open-source automatic card vending, digital goods storefront and bidirectional supply platform, shipped as a single binary.

> 前代版本：[ZCard 1.x](https://github.com/NovaWorks/ZCard)（PHP / Laravel），本仓库是它的 Golang 重写版本。

## 为什么开发 2.0

我们的 v1 版本基于 PHP 8.3 + Laravel，功能完整，也仍在维护。但在长期运营中，一些架构层面的问题反复出现：

- 部署环节多：PHP 版本与扩展、Composer、PHP-FPM、Supervisor、cron 都要正确配置，任何一环出问题都会导致服务异常，排查成本高；
- 后台队列是硬依赖：队列进程一旦没有运行，用户付款后无法拿到卡密，这类故障在低配服务器上并不少见；
- 升级麻烦：在线更新后需要手动重启 PHP-FPM 和 Supervisor，出问题回退旧版本全靠手工；
- 必须 MySQL：轻量场景下也希望用 SQLite 直接跑；
- 分站与分销功能互斥，无法同时使用。

2.0 用 Golang 重写，就是为了解决这些问题：

| | v1 | v2 |
|---|---|---|
| 部署 | PHP + Composer + PHP-FPM + Supervisor + cron | 单个二进制文件（前端内嵌） |
| 后台任务 | 队列强依赖 | Redis 可选，缺失时自动降级为同步执行 |
| 升级 | 手动重启服务，回退靠手工 | 后台一键更新，失败自动回滚 |
| 数据库 | MySQL | SQLite / MySQL / PostgreSQL |
| 分站与分销 | 互斥 | 可同时使用 |

选择 Golang 的原因：

- 编译为单个静态二进制，配合 `go:embed` 把前端打包进去，交叉编译一次就得到完整的安装包，部署就是复制一个文件；
- goroutine 并发模型适合上游同步、事件分发、队列消费这类异步工作，不需要常驻多个进程；
- 内存占用低，1核1G 的小服务器可以流畅运行；
- 静态类型加上架构守护测试（依赖方向、SQL 收口等由 CI 强制），项目长期迭代不容易腐化。

## 功能列表

### 双向上下游货源对接

**作为下游拿货**（对接异次元 ACG-Faka、独角数卡 Dujiao Next、其他 ZCard 站点）：
- 多货源连接管理、连接测试、商品预览勾选导入、分类映射一键建目录
- 全量 / 增量 / 定时同步（价格、库存、上下架，可设间隔与时间窗口防限流）
- 本地定价：比例加价、固定加价；封面采集本地化存储
- 付款后幂等下单拿货，失败自动进队列重试，可手动重试或转人工
- 下单前上游实时库存预检，防超卖

**作为上游供货**（开放 `/api/supply/*` API）：
- 下游账号申请 / 审核，api_key + api_secret
- 预存余额账本（充值、调账、余额快照、幂等流水）
- 商品级 / SKU 级专属供货价
- 幂等下单、未发货订单取消退款、回调通知
- 安全：HMAC-SHA256 四头签名、时间窗口、nonce 防重放、回调 SSRF 防护

**免修改对接**：供货 API 兼容主流发卡系统的上游对接协议——异次元、独角数卡等系统不需要修改任何代码，把 zcard-next 添加为上游货源即可正常拿货。

### 商品与卡密

- 树形分类：拖拽调整层级与排序、分类图标、递归聚合子分类商品
- 多规格 SKU，按规格定价与库存；会员等级价；库存继承
- 商品自定义表单控件（下单时收集买家信息）、标签、会员商品组、批量上下架
- 商品评价：审核、虚拟评价、展示开关
- 卡密批量导入（预览、确认、批次撤销）、keyed-hash 去重、批量导出（超管专属并审计）
- AES-256-GCM 加密存储（不可关闭），查看完整卡密需二次确认并留审计
- 单卡禁用/启用、靓号识别、低库存提醒

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
| Stripe | 信用卡，多币种 |
| PayPal | REST API |
| USDT | EpuSdt 协议，按链选择收款 |

- 一个支付渠道可承载多种支付方式，收银台按方式选择，支持自定义图标与描述
- 回调验签、幂等、金额核对；补单、退款（审计）；钱包余额支付

### 履约

- 本地卡密自动发货 / 固定内容直发（链接、兑换码）/ 人工发货 / 上游自动代发
- 付款后事件驱动自动交付，失败可追溯（采购单错误详情）、可重试

### 钱包与资金

- 余额、积分双账本；充值满赠档位；礼品卡兑换
- 提现：申请、审核、打款，手续费支持固定金额或比例，收款方式白名单
- 手动调账（超管专属并审计）；积分抵现比例与上限可配

### 增长运营

- 三级分销佣金（按订单金额或利润计佣，支持退款逆向扣回）
- 会员等级：消费积分升降级、等级专属价
- 优惠券、秒杀、促销活动、首页推荐位、顶部自定义按钮
- 分站白标：分站主绑定自有域名（DNS TXT / HTTP 双验证）、独立定价、订单快照与利润分账账本

### 工作台与报表

- 经营 KPI 总览、趋势图、热销排行、佣金列表
- 对账系统：对账任务创建与执行、逐笔明细核对
- 日结统计、流量统计（PV / UV、访问明细）

### 客服与触达

- 工单系统（前台提交、内部备注、解决/关闭流转）
- 邮件（SMTP）与短信（阿里云、腾讯云、七牛）模板通知、群发预估与取消

### 内容与前台

- 横幅、文章栏目、站点公告（文本/图片/轮播）
- 多模板体系（PC 与移动端独立模板，主题市场式安装）
- SEO：robots 与 sitemap 动态生成、SSG 预渲染、爬虫动态渲染，内容修改实时生效
- 多语言（中/英）、多币种切换；移动端全面适配

### 安全与权限

- RBAC 细粒度权限点（100+），敏感操作限超管（卡密明文、导出、资金操作）
- 操作日志、安全日志、访问统计三线审计
- 管理员 TOTP 两步验证、登录图形验证码、单 IP 待付款订单限流、风控黑名单
- 生产密钥环境变量注入，卡密加密密钥轮换工具（reencrypt-cards）

### 部署与在线更新

- 单二进制交付（管理后台 + 商城 + 数据库内嵌），双架构 amd64 / arm64
- 三种安装方式：一键脚本（自动 systemd + Nginx）、浏览器向导、命令行
- 后台一键在线更新：GitHub 直连 / 大陆加速镜像（自动探测切换）/ 自建静态源；ED25519 验签、更新前自动备份数据库、新版异常自动回滚、版本历史面板
- 后台任务调度内置（周期任务随服务启动，无需额外 cron）
- CLI 运维命令：install、serve、migrate、admin、self-update、dbtest、reencrypt-cards

## API 文档

全部接口（管理后台、前台、供货 API）由 protobuf 自动生成 OpenAPI 规范：[server/api/openapi.yaml](server/api/openapi.yaml)（244 个路径，随代码同步生成）。

供货 API（下游系统对接用）端点一览：

| 端点 | 说明 |
|---|---|
| `POST /api/supply/ping` | 连接测试 |
| `POST /api/supply/categories` | 商品分类 |
| `POST /api/supply/products` | 商品列表 |
| `POST /api/supply/products/{id}` | 商品详情 |
| `POST /api/supply/products/{id}/stock` | 实时库存 |
| `POST /api/supply/orders` | 幂等下单 |
| `POST /api/supply/orders/{id}` | 订单查询 |
| `POST /api/supply/orders/{id}/cancel` | 取消退款 |
| `POST /api/supply/orders/{id}/refund` | 申请退款 |

鉴权：HMAC-SHA256 签名（api_key + api_secret），请求带时间窗口与 nonce 防重放；具体签名算法与字段说明见 openapi.yaml 中 SupplyService 各接口描述。

## 技术栈

| 层 | 选型 |
|---|---|
| 后端 | Go、Kratos v3、Ent ORM、Atlas 版本化迁移、wire 依赖注入、protobuf 接口定义 |
| 管理后台 | Vue 3、Naive UI、UnoCSS（soybean-admin 定制） |
| 商城前台 | Vue 3、Tailwind CSS v4、vite-ssg 预渲染 |
| 存储 | SQLite（纯 Go 驱动）/ MySQL 8 / PostgreSQL 15+；Redis 可选 |
| 部署 | 单二进制、Docker、systemd 一键脚本、宝塔进程守护 |

## 项目结构

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
└── doc/                     # 部署指南
```

## 快速开始

```bash
# 方式一：一键脚本（Linux，自动配置 systemd 与 Nginx）
curl -fsSL https://raw.githubusercontent.com/NovaWorks/zcard-next/main/scripts/zcard-install.sh -o /tmp/zcard-install.sh
sudo bash /tmp/zcard-install.sh install --bin ./zcard-linux-amd64

# 方式二：手动部署，浏览器打开 http://IP:8000 进入安装向导
./zcard serve -conf configs

# 方式三：命令行安装
./zcard install
```

启动管理、反向代理、域名绑定与 HTTPS 配置见 [doc/部署指南.md](doc/部署指南.md)。

开发环境：

```bash
cd server && make init && make generate && make test && make run
```

## 持续更新

项目保持活跃开发：

- 功能与修复持续合入主干，每个可部署版本打 tag 发布（[Releases](../../releases)）
- 已部署实例在后台「设置 → 系统更新」一键升级，大陆服务器自动走加速镜像，升级失败自动回滚
- 支付驱动持续增加中，欢迎提 issue 建议需要对接的通道
- 欢迎提交 issue 与 PR

## 更多支持

- Telegram 群组：[@ZhonCard](https://t.me/ZhonCard)
- Telegram 频道：[@ZCardGroup](https://t.me/ZCardGroup)

## 与同类系统对比

| | ZCard 2.0 | dujiao-next | acg-faka |
|---|---|---|---|
| 安装 | 单二进制 + 浏览器向导 | 手编配置 + 管理脚本 | 浏览器向导（需填数据库） |
| 数据库 | 内嵌 SQLite，可选 MySQL/PG | SQLite | 必须 MySQL |
| Redis | 可选（自动降级） | 可选 | 无 |
| 在线更新 | 后台一键（验签、回滚、大陆加速） | — | — |
| 货源对接 | 双向（拿货 + 供货，免修改对接） | 单向拿货 | 单向拿货 |
| 对账系统 | 内置（任务 + 明细核对） | — | — |

## 工程纪律

- 金额统一使用 int64 分，禁止浮点
- 卡密强制加密存储，永不落明文
- 模块间只通过窄接口与事件通信，由架构测试强制
- 对外单号使用雪花 ID，与内部自增主键严格分离
- 三方言迁移独立版本化，启动自动迁移，失败拒绝启动
