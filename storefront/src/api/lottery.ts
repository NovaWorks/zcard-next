import { t as $t } from '@/i18n';
import { api } from "./client";
export interface LotteryPrize {
  id: number;
  name: string;
  image: string;
  mode: string;
  probability: number;
  remaining: number;
}
export interface LotteryActivity {
  id: number;
  name: string;
  description: string;
  image: string;
  status: string;
  start_at: number;
  end_at: number;
  timezone: string;
  chance_mode: string;
  chance_count: number;
  prizes: LotteryPrize[];
  revision: number;
}
export interface LotteryDraw {
  draw_no: string;
  activity_id: number;
  activity_name: string;
  prize_name: string;
  mode: string;
  status: string;
  created_at: number;
  delivered_at: number;
  content?: string;
}
export const lotteryStates: Record<string, string> = {
  get scheduled() { return $t("活动未开始"); },
  get live() { return $t("进行中"); },
  get paused() { return $t("活动已暂停"); },
  get ended() { return $t("活动已结束"); },
  get unavailable() { return $t("奖品暂不可用，等待平台补充"); },
  get pending() { return $t("待平台发放"); },
  get delivered() { return $t("已发放"); },
  get missed() { return $t("谢谢参与"); },
};
export const listLotteryActivities = () =>
  api.get<{ items: LotteryActivity[] }>("/lottery/activities");
export const getLotteryActivity = (id: number) =>
  api.get<LotteryActivity>("/lottery/activities/" + id);
export const claimLotteryChances = (id: number) =>
  api.post<{ remaining: number; period: string }>(
    "/lottery/activities/" + id + "/chances",
    {},
  );
export const drawLottery = (id: number, request_key: string) =>
  api.post<LotteryDraw>("/lottery/activities/" + id + "/draw", { request_key });
export const listMyLotteryDraws = (page = 1) =>
  api.get<{ items: LotteryDraw[]; total: number }>("/lottery/draws", {
    page,
    page_size: 20,
  });
export const getMyLotteryDraw = (no: string) =>
  api.get<LotteryDraw>("/lottery/draws/" + encodeURIComponent(no));
