import { request } from "../request";

// IDs and CAS versions deliberately remain decimal strings across the wire/UI.
export interface PluginStatus {
  plugin_id: string;
  desired_enabled: boolean;
  desired_generation: string;
  observed_generation: string;
  desired_digest: string;
  observed_digest: string;
  operation_id: string;
  phase: string;
  block_reasons: string[];
  reconciliation_pending: boolean;
  uninstalled: boolean;
  approved_scopes: string[];
  runtime_available: boolean;
}
export interface PluginCommand {
  plugin_id: string;
  operation_id: string;
  action: "import" | "enable" | "upgrade" | "rollback" | "disable" | "uninstall";
  target_digest: string;
  expected_generation: string;
  approved_scopes: string[];
}
export interface PluginOperation {
  operation_id: string;
  plugin_id: string;
  action: string;
  target_generation: string;
  phase: string;
  failure_code: string;
  status?: PluginStatus;
}
export interface PluginPackage {
  plugin_id: string;
  version: string;
  digest: string;
  scopes: string[];
}
export interface PluginConfig {
  schema_version: number;
  revision: string;
  enabled: boolean;
  allowed_level_ids: string[];
}
export interface PluginRule {
  plugin_id: string;
  product_id: string;
  required: boolean;
  requirement_revision: string;
  config?: PluginConfig;
  generation: string;
  config_valid: boolean;
  error_code: string;
}
export interface PluginSchema {
  plugin_id: string;
  generation: string;
  config_schema_json: string;
  ui_schema_json: string;
  runtime_available: boolean;
}
export interface PluginLevel {
  id: string;
  name: string;
  enabled: boolean;
}
export interface PluginImpact {
  affected_total: string;
  visible_product_ids: string[];
  truncated: boolean;
}
export interface PluginFiles {
  descriptor: File;
  signature: File;
  archive: File;
}
// Go's JSON codec omits proto3 zero values. Normalize only successful DTOs;
// request failures must never manufacture an empty or disabled configuration.
export function normalizePluginRule(rule: PluginRule): PluginRule {
  return {
    ...rule,
    required: rule.required ?? false,
    config_valid: rule.config_valid ?? false,
    config: rule.config
      ? {
          ...rule.config,
          enabled: rule.config.enabled ?? false,
          allowed_level_ids: rule.config.allowed_level_ids ?? [],
        }
      : undefined,
  };
}
const root = "/api/v1/admin";
const segment = encodeURIComponent;
const product = (id: string) => `${root}/products/${segment(id)}`;
const ruleURL = (productId: string, id: string) =>
  `${product(productId)}/plugin-rules/${segment(id)}`;
export const fetchPlugins = () =>
  request<{ plugins: PluginStatus[]; instance_management_allowed: boolean }>({
    url: `${root}/plugins`,
    silentError: true,
  });
export const fetchPluginOperation = (id: string) =>
  request<PluginOperation>({ url: `${root}/plugins/operations/${segment(id)}`, silentError: true });
export const fetchPluginImpact = (id: string) =>
  request<PluginImpact>({ url: `${root}/plugins/${segment(id)}/impact`, silentError: true });
export const operatePlugin = (data: PluginCommand) =>
  request<PluginOperation>({
    url: `${root}/plugins/${segment(data.plugin_id)}/operations`,
    method: "post",
    data,
    silentError: true,
  });
function upload<T>(action: "inspect" | "import", command: PluginCommand, files: PluginFiles) {
  const data = new FormData();
  data.append("command", JSON.stringify(command));
  for (const name of ["descriptor", "signature", "archive"] as const)
    data.append(name, files[name]);
  return request<T>({
    url: `${root}/plugins/${action}`,
    method: "post",
    data,
    headers: { "Content-Type": undefined },
    timeout: 60000,
    silentError: true,
  });
}
export const inspectPlugin = (command: PluginCommand, files: PluginFiles) =>
  upload<PluginPackage>("inspect", command, files);
export const importPlugin = (command: PluginCommand, files: PluginFiles) =>
  upload<PluginOperation>("import", command, files);
export const fetchPluginContributions = (id: string) =>
  request<{ contributions: PluginSchema[]; unavailable_reasons: string[] }>({
    url: `${root}/plugins/contributions`,
    params: { product_id: id },
    silentError: true,
  });
export const fetchProductPluginRules = (id: string) =>
  request<{ rules: PluginRule[] }>({ url: `${product(id)}/plugin-rules`, silentError: true });
export const fetchProductPluginRule = (id: string, pluginId: string) =>
  request<PluginRule>({ url: ruleURL(id, pluginId), silentError: true });
export const fetchPluginLevels = (id: string) =>
  request<{ options: PluginLevel[] }>({
    url: `${product(id)}/plugin-level-options`,
    silentError: true,
  });
export const savePluginRule = (rule: PluginRule, config: PluginConfig) =>
  request<PluginRule>({
    url: ruleURL(rule.product_id, rule.plugin_id),
    method: "put",
    silentError: true,
    data: {
      config,
      expected_generation: rule.generation,
      expected_config_revision: rule.config?.revision,
      expected_requirement_revision: rule.requirement_revision,
      schema_version: config.schema_version,
    },
  });
export const releasePluginRule = (rule: PluginRule) =>
  request<PluginRule>({
    url: `${ruleURL(rule.product_id, rule.plugin_id)}/release`,
    method: "post",
    silentError: true,
    data: { expected_requirement_revision: rule.requirement_revision },
  });

export interface MarketEntry {
  name: string;
  descriptor: { pluginId: string; version: string; archiveSHA256: string };
  manifest: { scopes: string[]; core: { minInclusive: string; maxExclusive: string } };
}
export interface MarketCatalog {
  catalog: { origin: string; revision: string; entries: MarketEntry[] };
  incompatible: Record<string, string>;
}
const marketRoot = `${root}/plugins/market`;
export const fetchMarketConfig = () =>
  request<{ origin: string }>({
    url: `${marketRoot}/config`,
    silentError: true,
  });
export const saveMarketConfig = (origin: string) =>
  request<{ origin: string }>({
    url: `${marketRoot}/config`,
    method: "put",
    data: { origin },
    silentError: true,
  });
export const fetchMarketCatalog = () =>
  request<MarketCatalog>({
    url: `${marketRoot}/catalog`,
    timeout: 30000,
    silentError: true,
  });
export const inspectMarketPlugin = (
  origin: string,
  version: string,
  command: PluginCommand,
) =>
  request<PluginPackage>({
    url: `${marketRoot}/inspect`,
    method: "post",
    data: { origin, version, command },
    timeout: 30000,
    silentError: true,
  });
export const installMarketPlugin = (
  origin: string,
  version: string,
  command: PluginCommand,
) =>
  request<PluginOperation>({
    url: `${marketRoot}/install`,
    method: "post",
    data: { origin, version, command },
    timeout: 30000,
    silentError: true,
  });
