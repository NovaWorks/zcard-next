package audit

// 管理面 API（P2-06）：三类日志查询、黑名单管理。

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	identityport "github.com/NovaWorks/zcard-next/server/internal/mods/identity/port"

	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminAuditService 管理面审计服务。
type AdminAuditService struct {
	adminv1.UnimplementedAdminAuditServiceServer
	repo   *AuditRepo
	admins identityport.AdminReader // 操作者/主体名称富化（nil = 跳过）
	users  identityport.UserReader  // 安全审计 user 主体名称富化（nil = 跳过）
}

// NewAdminAuditService 构造。
func NewAdminAuditService(repo *AuditRepo, admins identityport.AdminReader, users identityport.UserReader) *AdminAuditService {
	return &AdminAuditService{repo: repo, admins: admins, users: users}
}

// enrichAdminNames 员工 ID → 账号名（去重批量 PK get；失败项省略——前端回落 #ID）。
func (s *AdminAuditService) enrichAdminNames(ctx context.Context, ids ...uint64) map[uint64]string {
	out := make(map[uint64]string, len(ids))
	if s.admins == nil {
		return out
	}
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, done := out[id]; done {
			continue
		}
		if acc, err := s.admins.Admin(ctx, id); err == nil && acc != nil {
			out[id] = acc.Username
		}
	}
	return out
}

// enrichUserNames 前台用户 ID → 账号名（同上）。
func (s *AdminAuditService) enrichUserNames(ctx context.Context, ids ...uint64) map[uint64]string {
	out := make(map[uint64]string, len(ids))
	if s.users == nil {
		return out
	}
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, done := out[id]; done {
			continue
		}
		if name, err := s.users.Username(ctx, id); err == nil && name != "" {
			out[id] = name
		}
	}
	return out
}

// ListOpLogs 操作审计。
func (s *AdminAuditService) ListOpLogs(ctx context.Context, req *adminv1.ListOpLogsRequest) (*adminv1.ListOpLogsReply, error) {
	page, size := auditPageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListOpLogs(ctx, req.GetOperatorId(), page, size)
	if err != nil {
		return nil, err
	}
	adminIDs := make([]uint64, 0, len(rows))
	for _, l := range rows {
		if l.OperatorType == "admin" {
			adminIDs = append(adminIDs, l.OperatorID)
		}
	}
	names := s.enrichAdminNames(ctx, adminIDs...)
	reply := &adminv1.ListOpLogsReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, l := range rows {
		item := &adminv1.OpLogItem{
			Id: l.ID, OperatorType: string(l.OperatorType), OperatorId: l.OperatorID,
			OperatorName:  names[l.OperatorID],
			PermissionPoint: l.PermissionPoint, Action: l.Action, Route: l.Route,
			Ip: l.IP, CreatedAt: l.CreatedAt.Unix(),
		}
		if l.Before != nil {
			if b, err := json.Marshal(l.Before); err == nil {
				item.BeforeJson = string(b)
			}
		}
		if l.After != nil {
			if b, err := json.Marshal(l.After); err == nil {
				item.AfterJson = string(b)
			}
		}
		reply.Logs = append(reply.Logs, item)
	}
	return reply, nil
}

// ListSecurityLogs 安全审计。
func (s *AdminAuditService) ListSecurityLogs(ctx context.Context, req *adminv1.ListSecurityLogsRequest) (*adminv1.ListSecurityLogsReply, error) {
	page, size := auditPageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListSecurityLogs(ctx, req.GetAction(), page, size)
	if err != nil {
		return nil, err
	}
	adminIDs := make([]uint64, 0, len(rows))
	userIDs := make([]uint64, 0, len(rows))
	for _, l := range rows {
		switch l.ActorType {
		case "admin":
			adminIDs = append(adminIDs, l.ActorID)
		case "user":
			userIDs = append(userIDs, l.ActorID)
		}
	}
	adminNames := s.enrichAdminNames(ctx, adminIDs...)
	userNames := s.enrichUserNames(ctx, userIDs...)
	reply := &adminv1.ListSecurityLogsReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, l := range rows {
		name := adminNames[l.ActorID]
		if l.ActorType == "user" {
			name = userNames[l.ActorID]
		}
		item := &adminv1.SecurityLogItem{
			Id: l.ID, ActorType: string(l.ActorType), ActorId: l.ActorID,
			ActorName: name,
			Action: l.Action, Ip: l.IP, CreatedAt: l.CreatedAt.Unix(),
		}
		if l.Metadata != nil {
			if b, err := json.Marshal(l.Metadata); err == nil {
				item.MetadataJson = string(b)
			}
		}
		reply.Logs = append(reply.Logs, item)
	}
	return reply, nil
}

// ListVisitStats 访问统计。
func (s *AdminAuditService) ListVisitStats(ctx context.Context, req *adminv1.ListVisitStatsRequest) (*adminv1.ListVisitStatsReply, error) {
	page, size := auditPageParams(req.GetPage(), req.GetPageSize())
	date := req.GetStatDate()
	if date == "" {
		date = latestDate() // 最新日期（简化：当天）
	}
	rows, total, err := s.repo.ListVisitStats(ctx, date, page, size)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListVisitStatsReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, v := range rows {
		reply.Items = append(reply.Items, &adminv1.VisitStatItem{
			StatDate: v.StatDate, StatHour: int32(v.StatHour), Path: v.Path,
			Pv: v.Pv, Uv: v.Uv,
		})
	}
	return reply, nil
}

// GetBlacklist 黑名单。
func (s *AdminAuditService) GetBlacklist(ctx context.Context, _ *emptypb.Empty) (*adminv1.GetBlacklistReply, error) {
	return &adminv1.GetBlacklistReply{Entries: s.repo.BlacklistRaw()}, nil
}

// SetBlacklist 设置黑名单。
func (s *AdminAuditService) SetBlacklist(ctx context.Context, req *adminv1.SetBlacklistRequest) (*emptypb.Empty, error) {
	entries := strings.Split(req.GetEntries(), ",")
	cleaned := make([]string, 0, len(entries))
	for _, e := range entries {
		if e = strings.TrimSpace(e); e != "" {
			cleaned = append(cleaned, e)
		}
	}
	s.repo.SetBlacklist(cleaned)
	s.repo.Security(ctx, securityEntryOf("audit.blacklist_updated", map[string]any{"count": len(cleaned)}))
	return &emptypb.Empty{}, nil
}

func latestDate() string { return time.Now().UTC().Format("20060102") }

func auditPageParams(page, pageSize int32) (int, int) {
	p := int(page)
	if p < 1 {
		p = 1
	}
	ps := int(pageSize)
	if ps < 1 {
		ps = 20
	}
	if ps > 100 {
		ps = 100
	}
	return p, ps
}
