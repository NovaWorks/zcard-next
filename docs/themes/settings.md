# 主题设置（API v1）

后台入口：系统设置 → 模板 → 主题设置。也可以在“管理主题”中打开任意已安装主题的设置。

第一期提供分组表单、草稿、电脑/平板/手机只读预览、发布、恢复默认和恢复上一版。Classic 内置支持；云帆需要重新安装带设置定义的新主题包。没有设置定义的旧主题继续使用系统基础配置。

## 使用方式

- 调整后先打开预览，后续修改自动刷新预览。预览不切换商城主题，不写入业务数据，15 分钟后失效。
- 保存草稿：下次打开继续编辑，访客不受影响。
- 发布设置：当前主题立即使用新设置；未启用主题只保存其设置，不切换主题。
- 恢复默认：将该主题默认值写入草稿，检查后再发布。
- 恢复上一版：恢复前一次发布的配置及对应主题文件版本，立即生效。旧版本文件必须仍在服务器上。
- 页面提示设置被其他页面更新时，先保留自己的输入，再重新加载；不会直接覆盖其他管理员刚发布的设置。

主题设置覆盖系统“模板”中的对应外观值。购物车、支付用途、充值、分销、评价等业务规则仍由后端系统设置控制；主题的开关只控制界面入口。

配置保存在数据库，按站点和主题标识隔离，不写回 ZIP。切换主题不会删除配置；未发布过设置的站点升级后保持原有外观。主题文件升级时保留仍有效的字段，新增字段使用默认值，类型不兼容的字段回到默认值。没有数据库结构迁移。

## 主题开发者

在 `theme.json` 增加：

```json
{"schema_version":1,"key":"my-theme","name":"我的主题","version":"1.1.0","settings_schema":"settings.schema.json"}
```

完整示例见 [settings.schema.example.json](./settings.schema.example.json)。ZIP 内保留 `index.html`、编译静态资源和上述两个 JSON 文件；`schema_version` 仍为 1，设置协议版本由设置定义中的 `version: 1` 表示。主题设置运行时要求 ZCard v1.2.42 或更新版本。

每个分组使用唯一 `id` 和中文 `label`。字段包含 `key`、`label`、`type`、`default`，可选 `help`、`min`、`max`、`options`、`visible_when`、`capability` 和 `renamed_from`。

支持文本、多行文本、开关、整数、颜色、下拉选项、图片素材、商品和分类选择。商品与分类字段保存数字 ID，表单通过名称搜索；0 表示未选择。颜色为六位十六进制；图片允许本站 `/uploads/` 或 HTTP(S) URL。最多 20 个分组、120 个字段、128 KB 定义。后台只解释声明式字段，不执行主题提供的脚本或组件。

自定义字段使用 `theme.*`。允许复用的外观字段：`template.bg_image`、`bg_image_mobile`、`category_nav_style`、`default_view`、`per_row`、`per_page`、`sort_by`、`show_stock`、`show_sales`。其他系统字段不能通过主题设置覆盖。

字段改名时给新字段设置 `renamed_from: "theme.old_name"`，旧字段从当前定义移除；没有新值时沿用有效旧值。迁移为单次声明映射，不执行代码。更改字段类型时应提供合法默认值；如需保留不兼容类型，请在新版本中保留旧字段，另加新字段逐步迁移。

## 前端接入

框架无关的参考 SDK 位于 `packages/theme-sdk/src/index.ts`。第三方主题可复制到自己的源码，在应用启动及配置接口完成后调用 `mergeThemeConfig(entries)`。

服务器在应用脚本之前注入 `#zcard-theme-runtime`，内容为 JSON：主题标识、主题版本、配置版本、`values`、`capabilities`、`preview`。不要将 JSON 当作 HTML 渲染。公开配置接口也合并已发布值，草稿和历史版本不对外暴露。

SDK 会应用约定的 CSS 变量，但主题必须在自己的样式和组件里使用它们，单独添加设置定义不会自动改变界面。例如：

```css
.site-main { max-width: var(--zc-content-width, 1240px); }
.product-card { border-radius: var(--zc-card-radius, 20px); }
```

可用变量：`--zc-primary`、`--zc-content-width`、`--zc-font-size`、`--zc-card-radius`、`--zc-notice-width`、`--zc-article-width`。使用 `themeValue()` 或合并后的配置控制公告与入口，业务能力关闭时不得自行重新启用。

预览请求携带只读令牌，后端拒绝带令牌的写入请求。预览页面禁用表单提交、写入 fetch/XHR 与外链跳转，普通访客不携带令牌。主题业务请求应统一使用 fetch 并遵守 `preview` 标记；不要在 GET 请求中执行写入。静态主题和业务接口应同源。

本期不包含自由拖拽页面编辑器、模块排序、预设及配置导入导出。
