<p align="center">
  <img src=".github/logo.png" width="108" alt="ZCard" />
</p>

<h1 align="center">ZCard 2.0</h1>

<p align="center">
  <strong>数字商品销售 · 自动发卡 · 双向上下游供货</strong><br />
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
  <a href="#api-文档">API 文档</a> ·
  <a href="https://github.com/NovaWorks/zcard-next/releases">下载版本</a>
</p>

<p align="center">
  <sub>Open-source digital goods storefront and bidirectional supply platform.</sub>
</p>

ZCard 既能对接异次元、独角数卡和其他 ZCard 站点自动拿货，也能开放供货 API 向下游系统供货。支持 SQLite、MySQL、PostgreSQL，以及多语言、多货币、三级分销和分站白标。

前代版本：[ZCard 1.x](https://github.com/NovaWorks/ZCard)（PHP / Laravel）。本仓库 `zcard-next` 是它的 Go 重写版本。

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
> 请备份配置、密钥和数据。单二进制部署支持程序内在线更新；**Docker 通过重建镜像升级**。旧部署请先阅读部署指南中的迁移说明。

## 功能概览

| 能力 | 主要功能 |
| --- | --- |
| **双向货源** | 上游拿货、下游供货、价格与库存同步、兼容主流供货协议 |
| **商品与交易** | 多规格商品、加密卡密、购物车、多种支付方式、自动履约 |
| **会员与资金** | 会员等级、钱包、积分、优惠券、提现与退款 |
| **增长与运营** | 三级分销、分站白标、促销秒杀、经营报表与对账 |
| **内容与服务** | 多主题、多语言、多货币、SEO、工单及邮件短信通知 |
| **安全与运维** | RBAC、两步验证、操作审计、数据备份、单二进制与 Docker 部署 |

展开查看各模块的详细功能：

<details>
<summary><strong>货源、商品与卡密</strong></summary>

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

</details>

<details>
<summary><strong>订单、支付与履约</strong></summary>

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

</details>

<details>
<summary><strong>运营、内容与客户服务</strong></summary>

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

</details>

<details>
<summary><strong>安全、部署与在线更新</strong></summary>

### 安全与权限

- RBAC 细粒度权限点（100+），敏感操作限超管（卡密明文、导出、资金操作）
- 操作日志、安全日志、访问统计三线审计
- 管理员 TOTP 两步验证、登录图形验证码、单 IP 待付款订单限流、风控黑名单
- 生产密钥环境变量注入，卡密加密密钥轮换工具（reencrypt-cards）

### 部署与在线更新

- 单二进制交付（管理后台 + 商城 + SQLite 驱动内嵌），双架构 amd64 / arm64
- 多种安装方式：一键脚本（systemd）、Docker Compose、浏览器向导、命令行
- 后台一键在线更新（单二进制部署）：GitHub 直连 / 大陆加速镜像（自动探测切换）/ 自建静态源；ED25519 验签、更新前自动备份数据库、新版异常自动回滚、版本历史面板
- 后台任务调度内置（周期任务随服务启动，无需额外 cron）
- CLI 运维命令：install、serve、migrate、admin、self-update、dbtest、reencrypt-cards

</details>

## API 文档

全部接口（管理后台、前台、供货 API）由 protobuf 自动生成 OpenAPI 规范：[server/api/openapi.yaml](server/api/openapi.yaml)（随代码同步生成）。

<details>
<summary><strong>供货 API 端点与鉴权说明</strong></summary>

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

</details>

## 为什么开发 2.0

2.0 将商城、后台和内置任务调度集中到单个 Go 程序中，支持多种数据库，简化安装与运维。

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
| 升级 | 手动重启服务，回退靠手工 | 后台一键更新，失败自动回滚 |
| 数据库 | MySQL | SQLite / MySQL / PostgreSQL |
| 分站与分销 | 互斥 | 可同时使用 |

选择 Golang 的原因：

- 编译为单个静态二进制，配合 `go:embed` 把前端打包进去，交叉编译一次就得到完整的安装包，部署就是复制一个文件；
- goroutine 并发模型适合上游同步、事件分发、队列消费这类异步工作，不需要常驻多个进程；
- 内存占用低，1核1G 的小服务器可以流畅运行；
- 静态类型加上架构守护测试（依赖方向、SQL 收口等由 CI 强制），项目长期迭代不容易腐化。

</details>

<details>
<summary><strong>与同类系统对比</strong></summary>

| 对比项 | ZCard 2.0 | dujiao-next | acg-faka |
|---|---|---|---|
| 安装 | 单二进制 + 浏览器向导 | 手编配置 + 管理脚本 | 浏览器向导（需填数据库） |
| 数据库 | 内嵌 SQLite，可选 MySQL/PG | SQLite | 必须 MySQL |
| Redis | 可选（自动降级） | 可选 | 无 |
| 在线更新 | 后台一键（验签、回滚、大陆加速） | — | — |
| 货源对接 | 双向（拿货 + 供货，免修改对接） | 单向拿货 | 单向拿货 |
| 对账系统 | 内置（任务 + 明细核对） | — | — |

</details>

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
└── doc/                     # 部署指南
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

## 更新与交流

- **版本发布**：[GitHub Releases](https://github.com/NovaWorks/zcard-next/releases) 提供可部署版本与更新说明。
- **升级指引**：单二进制可在后台「设置 → 系统更新」升级；Docker 重建镜像。操作步骤与回滚边界见 [部署指南](doc/部署指南.md)。
- **问题反馈**：欢迎通过 [Issues](https://github.com/NovaWorks/zcard-next/issues) 报告问题、提出功能建议，或提交 PR。

| 交流讨论 | 更新通知 |
| --- | --- |
| [Telegram 群组 · @ZhonCard](https://t.me/ZhonCard) | [Telegram 频道 · @ZCardGroup](https://t.me/ZCardGroup) |
