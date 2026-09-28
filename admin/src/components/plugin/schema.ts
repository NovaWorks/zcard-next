import configSchema from "./config-schema-v1.json";
import type { PluginConfig, PluginSchema } from "@/service/api/plugin";

export interface PluginField {
  key: "enabled" | "allowedLevelIds";
  type: "switch" | "multiselect";
  label: string;
  optionsSource: string;
}
export const decimalID = (value: unknown): value is string =>
  typeof value === "string" &&
  /^[1-9][0-9]{0,19}$/.test(value) &&
  BigInt(value) <= 18446744073709551615n;
export function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  if (value && typeof value === "object")
    return `{${Object.entries(value)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([k, v]) => `${JSON.stringify(k)}:${canonical(v)}`)
      .join(",")}}`;
  return JSON.stringify(value);
}
// v1 is a frozen host contract, not an arbitrary JSON-schema/expression engine.
// Reject unknown constraints/controls rather than dropping data on save.
export function parsePluginFields(schema: PluginSchema): PluginField[] | null {
  try {
    if (canonical(JSON.parse(schema.config_schema_json)) !== canonical(configSchema)) return null;
    const contributions = JSON.parse(schema.ui_schema_json);
    if (!Array.isArray(contributions)) return null;
    const selected = contributions.filter(
      (c) => c.extensionPoint === "admin.product.edit.extra.v1",
    );
    if (selected.length !== 1) return null;
    const fields = selected[0].fields;
    if (!Array.isArray(fields) || fields.length !== 2) return null;
    if (new Set(fields.map((f) => f.key)).size !== 2) return null;
    for (const field of fields) {
      if (
        Object.keys(field).sort().join(",") !== "key,label,optionsSource,type" ||
        typeof field.label !== "string" ||
        !field.label ||
        field.label.length > 80
      )
        return null;
      if (field.key === "enabled" && field.type === "switch" && field.optionsSource === "none")
        continue;
      if (
        field.key === "allowedLevelIds" &&
        field.type === "multiselect" &&
        field.optionsSource === "member-level-options.v1"
      )
        continue;
      return null;
    }
    return fields;
  } catch {
    return null;
  }
}
export function validPluginConfig(config: PluginConfig) {
  return (
    config.schema_version === 1 &&
    typeof config.enabled === "boolean" &&
    Array.isArray(config.allowed_level_ids) &&
    config.allowed_level_ids.length <= 100 &&
    (!config.enabled || config.allowed_level_ids.length > 0) &&
    config.allowed_level_ids.every(decimalID) &&
    new Set(config.allowed_level_ids).size === config.allowed_level_ids.length
  );
}
export function errorReason(error: unknown): string {
  return (error as { response?: { data?: { reason?: string } } })?.response?.data?.reason || "";
}
