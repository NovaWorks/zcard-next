// Production admin bundle with local synthetic API responses.
const fs = require("node:fs");
const path = require("node:path");
const http = require("node:http");
const assert = require("node:assert/strict");
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || "@playwright/test");
const root = process.env.ZCARD_ADMIN_DIST || path.resolve(__dirname, "../admin/dist");
const output = process.env.ZCARD_TEST_OUTPUT || "/tmp/zcard-catalog-workspace";
fs.mkdirSync(output, { recursive: true });
const server = http.createServer((req, res) => {
  let file = path.join(root, new URL(req.url, "http://localhost").pathname.replace(/^\/admin\/?/, ""));
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, "index.html");
  res.setHeader("Content-Type", ({ ".html": "text/html", ".js": "application/javascript", ".css": "text/css" })[path.extname(file)] || "application/octet-stream");
  res.end(fs.readFileSync(file));
});

(async () => {
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}),
  });
  try {
    for (const [width, height] of [[1366, 768], [1280, 720], [1024, 768], [390, 844]]) {
      const page = await browser.newPage({ viewport: { width, height } });
      const errors = [];
      const queries = [];
      page.on("pageerror", error => errors.push(error.message));
      await page.addInitScript(() => localStorage.setItem("token", JSON.stringify("fixture")));
      await page.route("**/api/v1/**", async route => {
        const url = new URL(route.request().url());
        let data = { items: [], categories: [], connections: [], total: 0 };
        if (url.pathname.endsWith("/auth/profile")) data = { admin: { id: 1, username: "fixture" }, permissions: ["*"] };
        if (url.pathname.endsWith("/categories")) data = { categories: [{ id: 1, name: "社交商品" }, { id: 2, parent_id: 1, name: "频道服务" }] };
        if (url.pathname.endsWith("/products")) {
          queries.push(url.searchParams);
          data = {
            products: Array.from({ length: 20 }, (_, i) => ({
              id: i + 1, name: "商品 " + (i + 1), category_id: 2,
              price_cents: 1980, factory_price_cents: 1000, status: 1,
              stock_type: "card", stock: 72, stock_status: "current",
              upstream_source_id: 1, fulfillment_mode: "upstream",
            })),
            total: 80,
          };
        }
        await route.fulfill({ json: data });
      });
      await page.goto(`http://127.0.0.1:${server.address().port}/admin/product`);
      await page.locator("tr.n-data-table-tr").first().waitFor();
      const table = page.locator(".product-list-card .n-data-table");
      await expect.poll(async () => (await table.boundingBox()).height).toBeGreaterThanOrEqual(259);
      const visibleRows = await table.evaluate(el => {
        const bounds = el.getBoundingClientRect();
        return [...el.querySelectorAll("tbody tr.n-data-table-tr")].filter(row => {
          const box = row.getBoundingClientRect();
          return box.top >= bounds.top && box.bottom <= bounds.bottom;
        }).length;
      });
      assert(visibleRows >= 3, `${width}px shows only ${visibleRows} complete product rows`);
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "page overflow");

      const categoryCard = page.locator(".product-category-card");
      if (width >= 1024) {
        const original = await table.boundingBox();
        await page.getByRole("button", { name: "收起分类", exact: true }).click();
        await expect(categoryCard).toBeHidden();
        assert((await table.boundingBox()).width > original.width + 190, "collapsing categories must expand the table");
      }
      await page.getByRole("button", { name: "展开分类", exact: true }).click();
      await expect(categoryCard).toBeVisible();
      const search = page.getByPlaceholder("搜索商品名", { exact: true });
      await search.fill("加拿大");
      await search.press("Enter");
      await expect.poll(() => queries.at(-1).get("keyword")).toBe("加拿大");
      await categoryCard.getByText("社交商品", { exact: true }).click();
      await expect(search).toHaveValue("");
      await expect.poll(() => queries.at(-1).get("category_id")).toBe("1");
      assert(!queries.at(-1).get("keyword"));
      await expect(table).not.toHaveClass(/n-data-table--loading/);
      await expect(page.getByRole("button", { name: "批量上架", exact: true })).toBeHidden();

      await page.getByRole("button", { name: "收起分类", exact: true }).click();
      await page.getByRole("button", { name: "更多操作：商品 1", exact: true }).click();
      await expect(page.getByRole("menuitem", { name: "发货设置", exact: true })).toBeVisible();
      await expect(page.getByRole("menuitem", { name: "锁定", exact: true })).toBeVisible();
      await page.keyboard.press("Escape");
      await page.getByRole("button", { name: "更多筛选", exact: true }).click();
      await expect(page.getByText("全部锁定状态", { exact: true })).toBeVisible();
      await page.getByRole("button", { name: "仅自营", exact: true }).click();
      await page.getByRole("button", { name: "收起筛选（1）", exact: true }).click();
      await expect(page.getByRole("button", { name: "更多筛选（1）", exact: true })).toBeVisible();
      await page.screenshot({ path: path.join(output, `workspace-${width}.png`) });
      assert.deepEqual(errors, []);
      console.log(JSON.stringify({ width, height, visibleRows, categoryCollapse: true, searchCleared: true, menuAccessible: true, activeFilterVisible: true }));
      await page.close();
    }
  } finally {
    await browser.close();
    server.close();
  }
})().catch(error => {
  console.error(error);
  server.close();
  process.exitCode = 1;
});
