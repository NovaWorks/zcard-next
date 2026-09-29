export type ShippingAddress = Record<
  "country" | "region" | "city" | "district" | "address" | "name" | "phone" | "postal_code",
  string
>;
export type RegionData = Record<string, Record<string, string>>;
export const emptyAddress = (): ShippingAddress => ({
  country: "",
  region: "",
  city: "",
  district: "",
  address: "",
  name: "",
  phone: "",
  postal_code: "",
});
export function countryOptions(data: RegionData, allowed?: string[]) {
  const names = new Intl.DisplayNames(["zh-CN"], { type: "region" });
  return Object.keys(data)
    .filter((code) => !allowed || allowed.includes(code))
    .map((value) => ({
      value,
      label: `${names.of(value) || data[value].name || value} (${value})`,
    }))
    .sort((a, b) => a.label.localeCompare(b.label, "zh-CN"));
}
export function regionOptions(data: RegionData, country: string) {
  const info = data[country] || {};
  const keys = (info.sub_keys || "").split("~").filter(Boolean);
  const names = (info.sub_names || info.sub_lnames || "").split("~");
  return keys.map((value, i) => ({ value, label: names[i] || value }));
}
export function shippingStatus(value?: string) {
  return (
    (
      {
        none: "无需配送",
        pending_payment: "待付款",
        pending: "待发货",
        partial: "部分发货",
        shipped: "已发货，待收货",
        received: "已收货",
        canceled: "配送已取消",
      } as Record<string, string>
    )[value || "none"] || value
  );
}
export interface Shipment {
  id: number;
  carrier: string;
  tracking_no: string;
  items: Record<string, number>;
  status: string;
  created_at: number;
  received_at: number;
}
export function shipments(raw?: string): Shipment[] {
  try {
    const rows = JSON.parse(raw || "[]");
    return Array.isArray(rows) ? rows : [];
  } catch {
    return [];
  }
}
