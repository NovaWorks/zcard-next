import { request } from "../request";

// ── 工单工作台（ticket:read 查看 / ticket:write 回复·解决·关闭）──

export function fetchTickets(params?: { status?: string; type?: string; order_no?: string; page?: number; page_size?: number }) {
  return request({
    url: "/api/v1/admin/tickets",
    method: "get",
    params,
  });
}

export function fetchTicket(ticketNo: string) {
  return request({
    url: `/api/v1/admin/tickets/${ticketNo}`,
  });
}

export function replyTicket(ticketNo: string, data: { content: string; is_internal?: boolean }) {
  return request({
    url: `/api/v1/admin/tickets/${ticketNo}/reply`,
    method: "post",
    data,
  });
}

export function resolveTicket(ticketNo: string) {
  return request({
    url: `/api/v1/admin/tickets/${ticketNo}/resolve`,
    method: "post",
  });
}

export function closeTicket(ticketNo: string) {
  return request({
    url: `/api/v1/admin/tickets/${ticketNo}/close`,
    method: "post",
  });
}
