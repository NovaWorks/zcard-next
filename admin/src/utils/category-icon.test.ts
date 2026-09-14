import assert from "node:assert/strict";
import test from "node:test";
import { categoryIconImagePath } from "./category-icon";
import { hasRoutePermission } from "../router/permissions";

test("category image paths support uploaded, legacy relative and external images", () => {
  for (const [input, expected] of [
    ["/uploads/2026/09/icon.png", "/uploads/2026/09/icon.png"],
    ["uploads/2026/09/icon.png", "/uploads/2026/09/icon.png"],
    ["./uploads/icon.png", "/uploads/icon.png"],
    ["icon.webp?v=2#preview", "/icon.webp?v=2#preview"],
    ["https://cdn.example.test/icon", "https://cdn.example.test/icon"],
    ["//cdn.example.test/icon.svg", "//cdn.example.test/icon.svg"],
    ["data:image/png;base64,AA==", "data:image/png;base64,AA=="],
  ]) assert.equal(categoryIconImagePath(input), expected);
  for (const input of [undefined, "", "🏷️", "📧"]) assert.equal(categoryIconImagePath(input), "");
});

test("category page requires category access independently of product access", () => {
  assert.equal(hasRoutePermission("category", []), false);
  assert.equal(hasRoutePermission("category", ["catalog:read"]), false);
  assert.equal(hasRoutePermission("category", ["catalog:category_read"]), true);
  assert.equal(hasRoutePermission("category", ["*"]), true);
});
