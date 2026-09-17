// Start admin Vite on 5198, then run with PLAYWRIGHT_MODULE set if needed.
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || "@playwright/test");
const { readFileSync, mkdirSync } = require("node:fs");
const { resolve } = require("node:path");
const assert = require("node:assert/strict");
const output = process.env.ZCARD_TEST_OUTPUT || "/tmp/zcard-video-editor";
mkdirSync(output, { recursive: true });
const video = readFileSync(
  resolve(__dirname, "../server/internal/mods/media/testdata/tutorial.mp4"),
);
(async () => {
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}),
  });
  try {
    for (const width of [1280, 390]) {
      const page = await browser.newPage({ viewport: { width, height: 900 } });
      const errors = [];
      page.on("pageerror", (e) => {
        errors.push(e.message);
        console.error("PAGE ERROR", e.message);
      });
      let uploads = 0;
      await page.route("**/api/v1/**", async (route) => {
        const req = route.request(),
          url = new URL(req.url());
        if (url.pathname.endsWith("/media/video")) {
          assert.match(req.headers()["content-type"], /^multipart\/form-data; boundary=/);
          assert(req.postDataBuffer().includes(video));
          uploads++;
          return route.fulfill({
            json: { id: 2, url: "/uploads/tutorial.mp4", mime: "video/mp4", name: "tutorial.mp4" },
          });
        }
        if (url.pathname.endsWith("/media"))
          return route.fulfill({
            json: {
              items:
                url.searchParams.get("kind") === "video"
                  ? [
                      {
                        id: 2,
                        url: "/uploads/tutorial.mp4",
                        mime: "video/mp4",
                        name: "tutorial.mp4",
                      },
                    ]
                  : [
                      {
                        id: 1,
                        url: "/uploads/cover.svg",
                        mime: "image/svg+xml",
                        name: "cover.svg",
                      },
                    ],
              total: 1,
              max_video_bytes: 104857600,
            },
          });
        return route.fulfill({ json: { categories: [] } });
      });
      await page.route("**/uploads/tutorial.mp4", (route) =>
        route.fulfill({ contentType: "video/mp4", body: video }),
      );
      await page.route("https://video.example/tutorial.mp4", (route) =>
        route.fulfill({ contentType: "video/mp4", body: video }),
      );
      await page.route("**/uploads/cover.svg", (route) =>
        route.fulfill({
          contentType: "image/svg+xml",
          body: '<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><rect width="100" height="100" fill="blue"/></svg>',
        }),
      );
      await page.goto("http://127.0.0.1:5198/admin/tests/video-editor/index.html");
      await expect(page.getByRole("heading", { name: "视频编辑器回归" })).toBeVisible();
      await page.locator("[contenteditable=true]").click();
      await expect(page.locator('button[data-menu-key="zcVideo"]')).not.toHaveClass(/disabled/);
      await page.locator('button[data-menu-key="zcVideo"]').click();
      await page
        .getByPlaceholder("HTTPS 视频文件直链（不支持平台播放页面）")
        .fill("https://video.example/tutorial.mp4");
      await page.getByPlaceholder("选择图片或填写 HTTPS 图片地址").fill("/uploads/cover.svg");
      await expect(page.getByRole("button", { name: "插入正文", exact: true })).toBeEnabled();
      await page.getByRole("button", { name: "插入正文", exact: true }).click();
      await expect(page.locator("#result")).toHaveValue(/video\.example\/tutorial\.mp4/);
      await page.getByText("Markdown", { exact: true }).click();
      await expect(page.locator("#result")).toHaveValue(/poster=/);
      await page.getByText("HTML源码", { exact: true }).click();
      await page.getByText("所见即所得", { exact: true }).click();
      await expect(page.locator("#result")).toHaveValue(/video\.example\/tutorial\.mp4/);
      await expect(page.locator("#result")).toHaveValue(/cover\.svg/);
      await expect(page.locator(".w-e-text-container video")).toHaveCount(1);
      await expect(page.locator(".w-e-text-container video")).toHaveAttribute(
        "poster",
        "/uploads/cover.svg",
      );
      await page.locator("[contenteditable=true]").click();
      await expect(page.locator('button[data-menu-key="zcVideo"]')).not.toHaveClass(/disabled/);
      await page.locator('button[data-menu-key="zcVideo"]').click();
      await page
        .locator("input[type=file]")
        .last()
        .setInputFiles({ name: "tutorial.mp4", mimeType: "video/mp4", buffer: video });
      await expect(page.getByRole("button", { name: "插入正文", exact: true })).toBeEnabled();
      await page.getByRole("button", { name: "插入正文", exact: true }).click();
      await expect(page.locator("#result")).toHaveValue(/\/uploads\/tutorial\.mp4/);
      assert.equal(uploads, 1);
      await expect(page.getByPlaceholder("HTTPS 视频文件直链（不支持平台播放页面）")).toBeHidden();
      await page.screenshot({ path: output + "/editor-" + width + ".png", fullPage: true });
      assert.equal(errors.length, 0, errors.join("\n"));
      console.log("PASS video direct link, poster, Markdown round trip, multipart upload:", width);
      await page.close();
    }
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
