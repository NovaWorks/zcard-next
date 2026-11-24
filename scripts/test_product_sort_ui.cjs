// Production admin bundle with disposable API fixtures; never changes a live site.
const fs = require("node:fs");
const path = require("node:path");
const http = require("node:http");
const assert = require("node:assert/strict");
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || "@playwright/test");
const root = path.resolve(__dirname, "../admin/dist");
const output = process.env.ZCARD_TEST_OUTPUT || "/tmp/zcard-product-sort";
fs.mkdirSync(output, { recursive: true });
const server = http.createServer((req, res) => {
  let file = path.join(root, new URL(req.url, "http://localhost").pathname.replace(/^\/admin\/?/, ""));
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, "index.html");
  res.setHeader("Content-Type", ({ ".html": "text/html", ".js": "application/javascript", ".css": "text/css" })[path.extname(file)] || "application/octet-stream");
  res.end(fs.readFileSync(file));
});

(async () => {
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) });
  try {
    for (const width of [1440, 375]) {
      const page = await browser.newPage({ viewport: { width, height: 900 }, reducedMotion: "reduce" });
      page.setDefaultTimeout(10000);
      const errors = [], writes = [];
      let permissions = ["*"], failNext = true, lockOnSave = 0;
      const products = Array.from({ length: 25 }, (_, i) => ({
        id: i + 1, name: `商品${i + 1}`, sort: (i + 1) * 10, category_id: i < 20 ? 1 : 2,
        price_cents: 1980, factory_price_cents: 1000, points_required: 7,
        status: 1, stock_type: "card", stock: 10, stock_status: "current", is_locked: i === 2,
      }));
      page.on("pageerror", error => errors.push(error.message));
      await page.addInitScript(() => localStorage.setItem("token", JSON.stringify("fixture")));
      await page.route("**/api/v1/**", async route => {
        const req = route.request(), url = new URL(req.url());
        let data = { items: [], categories: [], connections: [], products: [], total: 0 };
        if (url.pathname.endsWith("/auth/profile")) data = { admin: { id: 1, username: "fixture" }, permissions };
        if (url.pathname.endsWith("/categories")) data = { categories: [{ id: 1, name: "第一分类" }, { id: 2, name: "第二分类" }] };
        if (url.pathname.endsWith("/products")) {
          const category = Number(url.searchParams.get("category_id")), pageNo = Number(url.searchParams.get("page") || 1), size = Number(url.searchParams.get("page_size") || 20);
          const rows = products.filter(p => !category || p.category_id === category).sort((a, b) => a.sort - b.sort || b.id - a.id);
          data = { products: rows.slice((pageNo - 1) * size, pageNo * size), total: rows.length };
        }
        if (url.pathname.endsWith("/batch-sort")) {
          const body = req.postDataJSON();
          writes.push(body);
          assert.equal(req.method(), "POST");
          assert.deepEqual(Object.keys(body), ["items"]);
          for (const item of body.items) assert.deepEqual(Object.keys(item).sort(), ["expected_sort", "id", "sort"]);
          if (failNext) {
            failNext = false;
            return route.fulfill({ status: 409, json: { code: 409, reason: "catalog.SORT_STALE", message: "排序已变化，请核对后重试" } });
          }
          // Another administrator locks a row after it was edited in the list.
          if (lockOnSave) products.find(p => p.id === lockOnSave).is_locked = true;
          let updated = 0, skipped_locked = 0;
          for (const item of body.items) {
            const p = products.find(p => p.id === item.id);
            if (p.is_locked) skipped_locked++;
            else { p.sort = item.sort; updated++; }
          }
          data = { updated, skipped_locked };
        }
        await route.fulfill({ json: data });
      });
      const input = id => page.getByRole("textbox", { name: `商品 商品${id} 的排序`, exact: true });
      const save = () => page.getByRole("button", { name: /^保存排序/ });
      await page.goto(`http://127.0.0.1:${server.address().port}/admin/product`);
      await expect(input(1)).toHaveValue("10");
      await expect(input(3)).toBeDisabled();
      assert(await input(1).evaluate(el => {
        const bounds = el.getBoundingClientRect();
        return document.elementFromPoint(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2) === el;
      }), "sort input must not be covered by the fixed action column");
      await input(1).fill("9");
      await input(2).fill("1");
      await expect(save()).toHaveText("保存排序（2）");
      await save().click();
      await expect(page.getByRole("alert").filter({ hasText: "排序已变化，请核对后重试" })).toBeVisible();
      await expect(input(1)).toHaveValue("9");
      await expect(input(2)).toHaveValue("1");
      await save().click();
      await expect(save()).toHaveCount(0);
      assert.deepEqual(writes[0], writes[1], "failed save must retain the exact drafts");
      assert.deepEqual(writes[1].items, [{ id: 1, sort: 9, expected_sort: 10 }, { id: 2, sort: 1, expected_sort: 20 }]);
      await expect(page.locator("tbody tr.n-data-table-tr").first()).toContainText("商品2");

      await input(1).fill("8");
      await page.getByTitle("最后一页", { exact: true }).click();
      await expect(input(21)).toHaveValue("210");
      await input(21).fill("5");
      await expect(page.getByText("其他页或筛选外 1 件", { exact: false })).toBeVisible();
      const categories = page.locator(".product-category-card");
      if (!(await categories.isVisible())) await page.getByRole("button", { name: "展开分类", exact: true }).click();
      await categories.getByText("第二分类", { exact: true }).click();
      await expect(input(21)).toHaveValue("5");
      await input(22).fill("6");
      await expect(save()).toHaveText("保存排序（3）");
      lockOnSave = 21;
      await save().click();
      await expect(save()).toHaveCount(0);
      await expect(input(21)).toHaveValue("210");
      await expect(input(21)).toBeDisabled();
      await expect(input(22)).toHaveValue("6");
      assert.equal(products.find(p => p.id === 1).sort, 8, "off-page draft must be included");
      assert(writes[2].items.length === 3);
      assert(products.every(p => p.price_cents === 1980 && p.factory_price_cents === 1000 && p.points_required === 7 && p.status === 1));

      await input(22).fill("");
      await expect(save()).toBeDisabled();
      await expect(page.getByRole("alert").filter({ hasText: "排序不能为空" })).toBeVisible();
      await page.getByRole("button", { name: "取消排序修改", exact: true }).click();
      await expect(input(22)).toHaveValue("6");
      await input(22).fill("1.5");
      await expect(save()).toBeDisabled();
      await page.getByRole("button", { name: "取消排序修改", exact: true }).click();
      await input(22).fill("0");
      await expect(save()).toBeEnabled();
      await page.getByRole("button", { name: "取消排序修改", exact: true }).click();
      assert.equal(writes.length, 3, "canceling must not submit");
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "page overflow");
      await page.screenshot({ path: path.join(output, `sort-${width}.png`), fullPage: true });

      if (width === 1440) {
        await input(22).fill("0");
        await page.getByRole("menuitem", { name: "商品分类", exact: true }).click();
        await expect(page.getByText("放弃未保存的排序？", { exact: true })).toBeVisible();
        await page.getByRole("button", { name: "继续编辑", exact: true }).click();
        await expect(input(22)).toHaveValue("0");
        assert(new URL(page.url()).pathname.endsWith("/product"));
        await page.getByRole("menuitem", { name: "商品分类", exact: true }).click();
        await page.getByRole("button", { name: "放弃修改", exact: true }).click();
        await expect(page).toHaveURL(/\/category$/);
        await page.getByRole("menuitem", { name: "商品管理", exact: true }).click();
        await expect(page).toHaveURL(/\/product$/);
        await expect(save()).toHaveCount(0);
      }

      permissions = ["catalog:read", "catalog:category_read"];
      await page.reload();
      await page.locator("tbody tr.n-data-table-tr").first().waitFor();
      await expect(page.getByRole("textbox", { name: /的排序$/ })).toHaveCount(0);
      assert.deepEqual(errors, []);
      console.log(JSON.stringify({ width, batchSave: true, draftRecovery: true, crossPage: true, categoryFilter: true, lockedSkip: true, validation: true, cancel: true, readOnly: true }));
      await page.close();
    }
  } finally {
    await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(error => { console.error(error); server.close(); process.exitCode = 1; });
