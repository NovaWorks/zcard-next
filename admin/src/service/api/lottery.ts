import { request } from "../request";
export interface LotteryPrize {
  id?: number;
  name: string;
  image: string;
  mode: string;
  product_id: number;
  sku_id: number;
  probability: number;
  quantity: number;
  issued?: number;
  content: string;
}
export interface LotteryActivity {
  id?: number;
  name: string;
  description: string;
  image: string;
  status: string;
  start_at: number;
  end_at: number;
  timezone: string;
  chance_mode: string;
  chance_count: number;
  revision: number;
  published?: boolean;
  prizes: LotteryPrize[];
}
export interface LotteryDraw {
  draw_no: string;
  user_id: number;
  username: string;
  activity_id: number;
  activity_name: string;
  prize_id: number;
  prize_name: string;
  mode: string;
  status: string;
  created_at: number;
  delivered_at: number;
  admin_id: number;
  remark: string;
  cost_cents: number;
  content?: string;
  revision: number;
}
const base = "/api/v1/admin/lottery";
export const listLotteryActivities = (params: Record<string, unknown> = {}) =>
  request<{ items: LotteryActivity[]; total: number }>({
    url: base + "/activities",
    params,
  });
export const getLotteryActivity = (id: number) =>
  request<LotteryActivity>({ url: base + "/activities/" + id });
export const saveLotteryActivity = (data: LotteryActivity) =>
  request<LotteryActivity>({ url: base + "/activities", method: "post", data });
export const setLotteryStatus = (
  id: number,
  status: string,
  revision: number,
) =>
  request<LotteryActivity>({
    url: base + "/activities/" + id + "/status",
    method: "post",
    data: { status, revision },
  });
export const grantLotteryChances = (
  id: number,
  data: { user_id: number; count: number; remark: string; request_key: string },
) =>
  request({ url: base + "/activities/" + id + "/grant", method: "post", data });
export const listLotteryDraws = (params: Record<string, unknown> = {}) =>
  request<{ items: LotteryDraw[]; total: number }>({
    url: base + "/draws",
    params,
  });
export const revealLotteryDraw = (no: string) =>
  request<LotteryDraw>({
    url: base + "/draws/" + no + "/reveal",
    method: "post",
  });
export const deliverLotteryDraw = (no: string, remark: string) =>
  request({
    url: base + "/draws/" + no + "/deliver",
    method: "post",
    data: { remark },
  });
export const listLotteryHistory = (id: number) =>
  request<{
    items: {
      revision: number;
      created_at: number;
      admin_id: number;
      snapshot_json: string;
    }[];
  }>({ url: base + "/activities/" + id + "/history" });
export const lotteryState: Record<string, string> = {
  draft: "草稿",
  scheduled: "未开始",
  live: "进行中",
  paused: "已暂停",
  ended: "已结束",
  archived: "已归档",
  unavailable: "奖品暂不可用",
  pending: "待平台发放",
  delivered: "已发放",
  missed: "未中奖",
};
export const lotteryMode: Record<string, string> = {
  card: "系统发卡",
  text: "系统发文字/链接",
  manual: "平台人工领取",
};
