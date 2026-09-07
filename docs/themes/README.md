# 响应式商城主题开发

PC 和手机共用一个主题，以 CSS 响应式布局适配屏幕。主题只负责商城页面，订单、支付、发货、登录和权限继续调用现有 `/api/v1/storefront/*` API。管理后台独立内嵌，不随商城主题切换。

运行时切换适用于项目的 **fullstack 单二进制部署**。`vite dev` 页面和另行部署的 Nginx/CDN 静态前台不经过此加载器，不会跟随后台选项切换。

## 从现有完整商城开始

推荐复制或修改 `storefront/`，保留路由、API 调用和支付处理，再调整布局及样式。不要把演示静态页面当成具备下单能力的完整商城。

在项目根目录执行：

```sh
pnpm --dir storefront install --frozen-lockfile
pnpm --dir storefront build:theme
python3 scripts/package-theme.py \
  --dist storefront/dist-theme \
  --key my-store \
  --name '我的商城主题' \
  --version 1.0.0 \
  --author 'Your Team' \
  --output /tmp/my-store-1.0.0.zip
```

`build:theme` 构建独立 SPA，资源使用 `./` 相对路径。常规 `build` 仍保留内置商城的 SSG 构建流程。主题包不能直接包含 `.vue`、`.ts`、`node_modules` 或其他服务端源码。

后台 **设置 → 模板 → 商城主题 → 选择主题 → 安装主题（zip）** 上传，选中主题卡片后点击弹窗中的 **切换为默认**。新打开或刷新的商城页面立即使用新主题，不需要重新编译或重启 Go；已打开页面继续使用当前版本资源。

## ZIP 结构与清单

```text
my-store/
  theme.json
  index.html
  preview.webp        # 可选
  assets/
    index-xxxx.js
    index-xxxx.css
```

也支持 ZIP 根目录直接放 `theme.json` 和 `index.html`，此时清单必须填写 `key`。

```json
{
  "schema_version": 1,
  "key": "my-store",
  "name": "我的商城主题",
  "version": "1.0.0",
  "author": "Your Team",
  "desc": "同时适配电脑与手机的响应式商城",
  "preview": "preview.webp"
}
```

- `key`：1–64 位小写字母、数字、下划线或连字符；首位为字母或数字。`classic` 是内置保留名称。
- `name`、`version` 必填；`schema_version` 当前为 1（省略视为 1）。这是包格式版本，不表示与未来全部 API 版本兼容。
- 有顶层目录时，目录名必须与清单 `key` 一致；不能打包多个主题目录。
- `preview` 可省略；填写时必须指向包内已存在图片，不能是外部 URL。
- 包体不超过 20MB，解压后不超过 100MB，总条目不超过 4000。反向代理的请求体限制需能容纳 Base64 JSON（20MB ZIP 约需 28MB 请求体）。
- 支持 HTML、JS/MJS、CSS、JSON、常见图片、Web 字体、WASM、TXT/XML/Webmanifest；不包含源码映射、可执行文件或隐藏文件。打包前关闭 source map。

## 资源地址与前端路由

服务端向主题首页 `<head>` 最前面注入：

```html
<base href="/templates/my-store/包内容摘要/">
```

因此：

1. Vite 使用 `base: './'`；不要在 HTML 手写 `<base>`，也不要使用 `/assets/xxx.js`。安装器会拒绝冲突的根绝对资源地址及缺失的首页引用资源。
2. **Vue Router 的路由 base 必须显式为 `/`**。资源 base 与业务路由 base 是两回事：

   ```ts
   createWebHistory('/')
   // vite-ssg: ViteSSG(App, { routes, base: '/' }, ...)
   ```

3. 业务链接用 `/products`、`/product/123`、`/login` 等站点根路径；请求 API 使用 `/api/v1/storefront/...`。不要用相对 `api/...` 或相对页面链接 `login`。
4. 用户刷新 `/product/123` 等路径时仍返回所选主题的 `index.html`，由前端路由接管。不要依赖主题内的服务端脚本或独立 SSR 进程。
5. `/api`、`/uploads`、`/payments`、`/health`、`/install`、后台入口及 `/templates` 为保留服务路径，不由主题接管。原有 SEO 爬虫渲染仍优先执行。
6. 页面不长期缓存；新主题资源按内容摘要隔离并可长期缓存。不要安装根作用域 Service Worker，否则旧缓存可能覆盖主题切换效果。

## 合并旧配置

后台只有一个主题选择器。设置表中的 `template.active_theme` 原子记录默认主题及其具体版本；切换时同步旧 `pc_template` 和 `mobile_template`，旧客户端单独写移动端键也会更新统一选择，无需改表。

- 旧 PC 值已设置：以 PC 为准（包括明确选择的 Classic）。
- 旧 PC 值缺失：沿用旧移动端值；两者都缺失则使用 Classic。
- 旧客户端一次提交两个不同主题：明确拒绝冲突，不按字段顺序覆盖。
- 所选主题缺失或无法读取：前台回落内嵌 Classic；后台可重新选择并保存。

## 安装与升级

新主题保存于运行工作目录下 `data/themes/<key>/<内容摘要>/`，`current` 文件记录最新安装版本，不代表正在使用的版本。先校验、再完整写入版本目录，最后原子替换 `current`；失败不会删除或替换正在使用的旧版。

上传仅安装并刷新主题列表，不会改变首页。同名主题上传新版也会继续使用原来的版本；选中卡片并点击「切换为默认」后，才将该主题最新安装版本保存为默认，新打开或刷新的商城页面随即生效。关闭弹窗不会切换。默认主题切换独立保存，不提交设置页的其他未保存修改。

旧版本目录会保留，使已打开的页面仍能加载原版分块文件。备份、Docker 持久卷和迁移应包含 `data/themes/`。不要删除默认主题正在使用的版本和最新安装版本；长期运行可在确认没有旧页面使用后清理历史版本。

旧 `web/storefront/templates/<key>/` 中包含合法 `theme.json`、`index.html` 和资源的主题仍可读取。只有元数据或预览图的旧包不是可运行主题，需要重新构建上传。Classic 始终在列表中，且不能被 ZIP 覆盖。

## 发布前验收

至少验证：首页、分类/商品详情、登录与游客下单、支付跳转/返回/查询、订单与卡密展示、购物车、个人中心，以及 320px/375px/桌面布局。支付验收使用可控测试环境；主题切换不应改变账务与发货逻辑。

主题内 JavaScript 与商城同源执行，安装权限仅授予可信管理员；只安装可信来源的包。升级商城版本前，应针对该版本 API 回归验证自定义主题。
