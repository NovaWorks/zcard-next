import { request } from "../request";

// ── 工作台 ──

export interface DashboardStat {
  orders: number;
  revenue: number;
  paid_orders: number;
}

export interface DashboardTrendPoint {
  date: string;
  orders: number;
  revenue: number;
}

export interface DashboardTopProduct {
  product_id: number;
  name: string;
  sold_qty: number;
  revenue: number;
}

export interface DashboardData {
  today: DashboardStat;
  last7d: DashboardStat;
  last30d: DashboardStat;
  trend: DashboardTrendPoint[];
  top_products: DashboardTopProduct[];
}

/** 获取工作台统计数据（金额单位为分） */
export function fetchDashboard() {
  return request({ url: "/api/v1/admin/dashboard" });
}
