import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { decimalID, parsePluginFields, validPluginConfig } from "./schema";
import configSchema from "./config-schema-v1.json";

const manifest = JSON.parse(
  readFileSync(
    new URL("../../../../examples/plugins/member-purchase-gate/manifest.json", import.meta.url),
    "utf8",
  ),
);
const schema = {
  plugin_id: manifest.id,
  generation: "9007199254740993",
  config_schema_json: JSON.stringify(configSchema),
  ui_schema_json: JSON.stringify(manifest.uiContributions),
  runtime_available: true,
};
test("frozen schema matches backend and rejects unknown controls/constraints", () => {
  const backend = JSON.parse(
    readFileSync(
      new URL(
        "../../../../server/internal/platform/plugincontract/schema/config.json",
        import.meta.url,
      ),
      "utf8",
    ),
  );
  assert.deepEqual(configSchema, backend);
  assert.equal(parsePluginFields(schema)?.length, 2);
  assert.equal(
    parsePluginFields({
      ...schema,
      ui_schema_json: schema.ui_schema_json.replace("multiselect", "html"),
    }),
    null,
  );
  assert.equal(
    parsePluginFields({
      ...schema,
      config_schema_json: schema.config_schema_json.replace('"const":1', '"const":2'),
    }),
    null,
  );
  assert.equal(parsePluginFields({ ...schema, ui_schema_json: "{" }), null);
});
test("large IDs remain exact strings and uint64 bounds are enforced", () => {
  assert(decimalID("9007199254740993"));
  assert(decimalID("18446744073709551615"));
  for (const invalid of [1, Number("9007199254740993"), "01", "0", "18446744073709551616"])
    assert(!decimalID(invalid));
  const config = {
    schema_version: 1,
    revision: "9007199254740993",
    enabled: true,
    allowed_level_ids: ["9007199254740993"],
  };
  assert(validPluginConfig(config));
  assert(!validPluginConfig({ ...config, allowed_level_ids: [] }));
  assert(!validPluginConfig({ ...config, allowed_level_ids: ["1", "1"] }));
});
