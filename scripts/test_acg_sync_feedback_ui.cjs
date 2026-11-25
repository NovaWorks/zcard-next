// Isolated admin API fixtures; no real supplier calls or customer data writes.
const fs = require("node:fs"), path = require("node:path"), http = require("node:http"), assert = require("node:assert/strict");
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || "@playwright/test");
const root = path.resolve(__dirname, "../admin/dist");
const output = process.env.ZCARD_TEST_OUTPUT || "/tmp/zcard-acg-sync-ui";
fs.mkdirSync(output, { recursive: true });
const server = http.createServer((req, res) => {
  let file = path.join(root, new URL(req.url, "http://localhost").pathname.replace(/^\/admin\/?/, ""));
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, "index.html");
  res.setHeader("Content-Type", ({ ".html": "text/html", ".js": "application/javascript", ".css": "text/css" })[path.extname(file)] || "application/octet-stream");
  res.end(fs.readFileSync(file));
});
(async () => {
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const browser = await chromium.launch({ headless: true });
  try {
    for (const width of [1440, 390]) {
      const page = await browser.newPage({ viewport: { width, height: 1050 } });
      const errors = [], requests = [];
      const connection = { id: 9, name: "ACG 验证渠道", driver: "acg_faka", base_url: "https://example.com", status: "active", exchange_rate: 1, settings: JSON.stringify({ category_map: { A: 1 }, schedule: { request_delay: 1 } }) };
      const tasks = [
        { id: 1, connection_id: 9, scope: "price", mode: "full", status: "failed", processed: 3, total: 3, updated: 2, price_updated: 2, error_code: "PARTIAL_SYNC_FAILED", error_context: "报价成功 2，失败 1，跳过不可采购 0；失败商品保留原价格" },
        { id: 2, connection_id: 9, scope: "status", mode: "full", status: "failed", processed: 3, total: 3, updated: 3, error_code: "PARTIAL_STOCK_FAILED", error_context: "商品同步已完成；库存成功 2，失败 1。可仅重试失败库存。" },
        { id: 3, connection_id: 9, scope: "price", mode: "full", status: "done", processed: 3, total: 3, updated: 2, manual_skipped: 1, error_context: "报价成功 2，跳过不可采购 1；跳过商品保留原价格" },
      ];
      page.on("pageerror", error => errors.push(error.message));
      await page.addInitScript(() => localStorage.setItem("token", JSON.stringify("fixture")));
      await page.route("**/api/**", async route => {
        const req = route.request(), url = new URL(req.url()), p = url.pathname;
        let data = { items: [], total: 0, categories: [], connections: [] };
        if (p.endsWith("/auth/profile")) data = { admin: { id: 1, username: "fixture" }, permissions: ["*"] };
        if (p.endsWith("/supply/connections")) data = { connections: [connection], total: 1 };
        if (p.endsWith("/sync-tasks")) {
          if (req.method() === "POST") requests.push(req.postDataJSON());
          data = { tasks, total: tasks.length };
        }
        if (p.endsWith("/supply/connections/9") && req.method() === "PUT") {
          const payload = req.postDataJSON();
          requests.push(payload);
          connection.settings = payload.settings;
          data = connection;
        }
        await route.fulfill({ json: data });
      });
      const openMenu = async name => {
        await page.getByRole("button", { name: /^(更多|操作)/ }).first().click();
        await page.getByText(name, { exact: true }).last().click();
      };
      await page.goto(`http://127.0.0.1:${server.address().port}/admin/channel`);
      await expect(page.getByText(connection.name, { exact: true })).toBeVisible();
      const taskButton = page.getByRole("button", { name: "任务", exact: true });
      if (await taskButton.count()) await taskButton.click();
      else await openMenu("同步任务");
      const drawer = page.locator(".n-drawer").last();
      await expect(drawer.getByText("部分完成", { exact: true })).toHaveCount(2);
      await expect(drawer.getByText("完成", { exact: true })).toBeVisible();
      await expect(drawer.getByText("报价成功 2，失败 1，跳过不可采购 0；失败商品保留原价格", { exact: true })).toBeVisible();
      await expect(drawer.getByText("报价成功 2，跳过不可采购 1；跳过商品保留原价格", { exact: true })).toBeVisible();
      await expect.poll(async () => {
        const box = await drawer.boundingBox();
        return box && box.x >= -1 && box.x + box.width <= width + 1;
      }).toBeTruthy();
      const summary = drawer.getByText("报价成功 2，失败 1，跳过不可采购 0；失败商品保留原价格", { exact: true });
      await summary.scrollIntoViewIfNeeded();
      await expect(summary).toBeInViewport();
      await page.screenshot({ path: path.join(output, `partial-${width}.png`), fullPage: true, animations: "disabled" });
      await drawer.getByRole("button", { name: "重试库存", exact: true }).click();
      assert.deepEqual(requests.at(-1), { connection_id: 9, scope: "stock", mode: "failed" });
      await page.keyboard.press("Escape");
      await expect(drawer).toHaveCount(0);
      await page.getByRole("button", { name: /^(更多|操作)/ }).first().click();
      await page.getByText(/定时计划/, { exact: false }).last().click();
      const modal = page.locator(".n-modal").filter({ hasText: "定时同步计划" });
      const quoteDelay = modal.getByRole("textbox", { name: "报价请求间隔（毫秒）", exact: true });
      await expect(quoteDelay).toHaveValue("500");
      await expect(modal).toContainText("上方分页间隔单位为秒");
      await quoteDelay.scrollIntoViewIfNeeded();
      await expect(quoteDelay).toBeInViewport();
      await page.screenshot({ path: path.join(output, `timing-${width}.png`), fullPage: true, animations: "disabled" });
      await quoteDelay.fill("750");
      await modal.getByRole("button", { name: "保存计划", exact: true }).click();
      await expect(modal).toHaveCount(0);
      const saved = JSON.parse(requests.at(-1).settings);
      assert.equal(saved.schedule.request_delay, 1);
      assert.equal(saved.schedule.quote_request_delay_ms, 750);
      assert.deepEqual(saved.category_map, { A: 1 });
      await openMenu("编辑");
      const edit = page.locator(".n-modal").filter({ hasText: "编辑渠道" });
      const preferred = edit.getByText("优先列表库存", { exact: true });
      await expect(preferred).toBeVisible();
      await preferred.scrollIntoViewIfNeeded();
      await page.screenshot({ path: path.join(output, `stock-source-${width}.png`), fullPage: true, animations: "disabled" });
      await preferred.click();
      await page.getByText("逐件实时补查", { exact: true }).last().click();
      await edit.getByRole("button", { name: "保存", exact: true }).click();
      await expect(edit).toHaveCount(0);
      const live = JSON.parse(requests.at(-1).settings);
      assert.equal(live.acg_stock_source, "live");
      assert.deepEqual(live.category_map, { A: 1 });
      assert.equal(live.schedule.quote_request_delay_ms, 750);
      await openMenu("编辑");
      const again = page.locator(".n-modal").filter({ hasText: "编辑渠道" });
      await again.getByText("逐件实时补查", { exact: true }).click();
      await page.getByText("优先列表库存", { exact: true }).last().click();
      await again.getByRole("button", { name: "保存", exact: true }).click();
      await expect(again).toHaveCount(0);
      assert.equal(JSON.parse(requests.at(-1).settings).acg_stock_source, "catalog");
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "page overflow");
      assert.deepEqual(errors, []);
      await page.close();
      console.log(JSON.stringify({ width, partialQuotes: true, partialStock: true, skippedVisible: true, stockOnlyRetry: true, millisecondsSaved: true, catalogStockSourceSaved: true }));
    }
  } finally { await browser.close(); await new Promise(resolve => server.close(resolve)); }
})().catch(error => { console.error(error); server.close(); process.exitCode = 1; });
