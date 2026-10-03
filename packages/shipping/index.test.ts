import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { addressFields, addressLines, addressRequired, countryOptions, emptyAddress, regionOptions, validateAddress, type RegionData } from "./index.ts";

const regions: RegionData = JSON.parse(readFileSync(new URL("../../server/internal/platform/shipping/regions.json", import.meta.url), "utf8"));

test("international fields follow each destination's requirements and format", () => {
  assert.deepEqual(addressFields(regions, "AU"), ["city", "region", "postal_code"]);
  assert.equal(addressRequired(regions, "AU", "postal_code"), true);
  assert.deepEqual(addressFields(regions, "GB"), ["city", "postal_code"]);
  assert.deepEqual(addressFields(regions, "HK"), ["city", "region"]);
  assert.equal(addressRequired(regions, "HK", "city"), false);
  assert.deepEqual(addressFields(regions, "SG"), ["postal_code"]);
  assert.deepEqual(addressFields(regions, "CN"), ["city", "district", "region", "postal_code"]);
});

test("localized country and state labels keep stable country and subdivision codes", () => {
  assert.match(countryOptions(regions, ["AU"], "en")[0].label, /Australia/);
  assert.match(countryOptions(regions, ["AU"], "zh-CN")[0].label, /澳大利亚/);
  assert.equal(regionOptions(regions, "CN", "en").find(state => state.value === "北京市")?.label, "Beijing Shi");
  assert.equal(regionOptions(regions, "HK", "en").find(state => state.value === "Kowloon")?.label, "Kowloon");
  assert.equal(regionOptions(regions, "HK", "zh-CN").find(state => state.value === "Kowloon")?.label, "九龍");
});

test("address second line stays independent and validates per field", () => {
  const address = { ...emptyAddress(), country: "AU", city: "Sydney", region: "NSW", postal_code: "2000", name: "Buyer", phone: "+61412345678", address: "3 Example Street", address_line2: "Unit 4" };
  assert.deepEqual(validateAddress(address, regions), {});
  assert.deepEqual(addressLines(address), ["3 Example Street", "Unit 4", "Sydney NSW 2000", "AU"]);
  address.postal_code = "ABCDE";
  assert.ok(validateAddress(address, regions).postal_code);
  address.address_line2 = "Unit 4\nLevel 2";
  assert.ok(validateAddress(address, regions).address_line2);
  address.postal_code = "";
  address.city = "";
  assert.ok(validateAddress(address, regions).city);
  assert.ok(validateAddress(address, regions).postal_code);
});
