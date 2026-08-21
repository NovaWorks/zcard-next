import { request } from "../request";

// ── 工作台 ──

export interface DashboardStat {
  orders: number;
  revenue: number;
  paid_orders: number;
  cost: number;
  profit: number;
  new_users: number;
}

export interface DashboardTrendPoint {
  date: string;
  orders: number;
  revenue: number;
  paid_count: number;
  cost: number;
  profit: number;
}

export interface DashboardTopProduct {
  product_id: number;
  name: string;
  sold_qty: number;
  revenue: number;
}

export interface DashboardTopChannel {
  channel: string;
  total_count: number;
  success_count: number;
  failed_count: number;
}

export interface DashboardPending {
  pending_withdrawals: number;
  pending_refunds: number;
  fulfilling_orders: number;
  low_stock_products: number;
}

export interface DashboardData {
  today: DashboardStat;
  yesterday: DashboardStat;
  last7d: DashboardStat;
  prev7d: DashboardStat;
  last30d: DashboardStat;
  prev30d: DashboardStat;
  trend: DashboardTrendPoint[];
  top_products: DashboardTopProduct[];
  top_channels: DashboardTopChannel[];
  pending: DashboardPending;
  online_users: number;
}

export interface TrafficPoint {
  date: string;
  pv: number;
  uv: number;
}

/** 获取工作台统计数据（金额单位为分；trendDays 趋势天数 7/14/30，默认 7） */
export function fetchDashboard(trendDays?: number) {
  return request<DashboardData>({
    url: "/api/v1/admin/dashboard",
    method: "get",
    params: trendDays ? { trend_days: trendDays } : undefined,
  });
}

/** 获取流量统计（PV/UV 按天；days 7/14/30，默认 7） */
export function fetchTraffic(days?: number) {
  return request<{ points: TrafficPoint[] }>({
    url: "/api/v1/admin/dashboard/traffic",
    method: "get",
    params: days ? { days } : undefined,
  });
}
