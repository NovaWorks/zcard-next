package identity

// AdminUserManageService 前台用户管理（列表/详情聚合/封禁/新增/重置密码）。
// 数据域 users 表（storefront 注册用户）；员工管理（admin_users）在 authz 模块。
// 分站隔离：users 表无 subsite 列（全站账户），管理面按全站口径查询。
//
// 聚合视图（等级/钱包/积分/优惠券/供货账户/订单）经共享 ent 层批量查询
// （与 fulfillment.ProductNames 同模式——模块纪律禁 import 对方 biz/data，
// 共享 data/ent 的只读聚合不在此列）；等级判定复用 memberlevel RateResolver。

import (
	"context"
	"strings"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pointaccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplieraccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/walletaccount"
	memberlevelport "github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminUserManageService 实现 adminv1.AdminUserManageService。
type AdminUserManageService struct {
	adminv1.UnimplementedAdminUserManageServiceServer
	repo *UserRepo
	rate memberlevelport.RateResolver // 会员等级解析（0=无等级）
}

// NewAdminUserManageService 构造。
func NewAdminUserManageService(repo *UserRepo, rate memberlevelport.RateResolver) *AdminUserManageService {
	return &AdminUserManageService{repo: repo, rate: rate}
}

// ListUsers 用户列表（关键词/状态/供货商筛选；逐条聚合等级/钱包/订单/供货标识）。
func (s *AdminUserManageService) ListUsers(ctx context.Context, req *adminv1.ListUsersRequest) (*adminv1.ListUsersReply, error) {
	q := data.Client(ctx, s.repo.data).User.Query()
	if kw := req.GetKeyword(); kw != "" {
		q = q.Where(user.Or(
			user.UsernameContainsFold(kw),
			user.EmailContainsFold(kw),
		))
	}
	if st := req.GetStatus(); st != "" {
		if !validUserStatus(st) {
			return nil, errors.BadRequest("identity.INVALID_STATUS", "状态仅支持 active/banned")
		}
		q = q.Where(user.StatusEQ(user.Status(st)))
	}
	client := data.Client(ctx, s.repo.data)
	if req.GetIsSupplier() {
		accts, err := client.SupplierAccount.Query().All(ctx)
		if err != nil {
			return nil, errors.InternalServer("identity.USER_LIST_FAILED", "读取用户列表失败")
		}
		sids := make([]uint64, 0, len(accts))
		for _, a := range accts {
			sids = append(sids, a.OwnerUserID)
		}
		q = q.Where(user.IDIn(sids...))
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, errors.InternalServer("identity.USER_LIST_FAILED", "读取用户列表失败")
	}
	page, size := normPage(req.GetPage(), req.GetPageSize())
	rows, err := q.
		Order(ent.Desc(user.FieldID)).
		Offset((page - 1) * size).
		Limit(size).
		All(ctx)
	if err != nil {
		return nil, errors.InternalServer("identity.USER_LIST_FAILED", "读取用户列表失败")
	}
	reply := &adminv1.ListUsersReply{Total: int64(total)}
	items := s.enrich(ctx, rows)
	for _, r := range rows {
		reply.Users = append(reply.Users, items[r.ID])
	}
	return reply, nil
}

// GetUser 用户详情（聚合：等级/钱包/优惠券/供货账户/邀请关系/最近订单）。
func (s *AdminUserManageService) GetUser(ctx context.Context, req *adminv1.GetUserRequest) (*adminv1.UserDetail, error) {
	client := data.Client(ctx, s.repo.data)
	row, err := client.User.Get(ctx, req.GetId())
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("identity.USER_NOT_FOUND", "用户不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("identity.USER_GET_FAILED", "读取用户失败")
	}
	item := s.enrich(ctx, []*ent.User{row})[row.ID]

	detail := &adminv1.UserDetail{User: item}
	// 持有优惠券（最多 50，未使用在前）
	coupons, _ := client.Coupon.Query().
		Where(coupon.UserIDEQ(row.ID)).
		Order(ent.Desc(coupon.FieldID)).
		Limit(50).
		All(ctx)
	for _, c := range coupons {
		detail.Coupons = append(detail.Coupons, &adminv1.UserCouponItem{
			Id: c.ID, Title: c.Name, Status: string(c.Status),
		})
	}
	// 最近订单 10 条
	orders, _ := client.Order.Query().
		Where(order.UserIDEQ(row.ID)).
		Order(ent.Desc(order.FieldID)).
		Limit(10).
		All(ctx)
	for _, o := range orders {
		ro := &adminv1.UserRecentOrder{
			OrderNo: o.OrderNo, AmountCents: o.TotalAmount, Status: string(o.Status),
			CreatedAt: o.CreatedAt.Unix(),
		}
		detail.RecentOrders = append(detail.RecentOrders, ro)
	}
	// 邀请关系
	if row.InviteL1 > 0 {
		if inviter, err := client.User.Get(ctx, row.InviteL1); err == nil {
			detail.InviterUsername = inviter.Username
		}
	}
	if n, err := client.User.Query().Where(user.InviteL1EQ(row.ID)).Count(ctx); err == nil {
		detail.InviteesCount = int64(n)
	}
	return detail, nil
}

// SetUserStatus 封禁/解封（deleted 不可经此设置）。
func (s *AdminUserManageService) SetUserStatus(ctx context.Context, req *adminv1.SetUserStatusRequest) (*adminv1.UserItem, error) {
	if !validUserStatus(req.GetStatus()) {
		return nil, errors.BadRequest("identity.INVALID_STATUS", "状态仅支持 active/banned")
	}
	row, err := data.Client(ctx, s.repo.data).User.UpdateOneID(req.GetId()).
		SetStatus(user.Status(req.GetStatus())).
		Save(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("identity.USER_NOT_FOUND", "用户不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("identity.USER_STATUS_FAILED", "更新用户状态失败")
	}
	return s.enrich(ctx, []*ent.User{row})[row.ID], nil
}

// CreateUser 后台新增用户（复用注册管线：校验/密码哈希/推广码全同源）。
func (s *AdminUserManageService) CreateUser(ctx context.Context, req *adminv1.CreateUserRequest) (*adminv1.UserItem, error) {
	if req.GetUsername() == "" || req.GetPassword() == "" {
		return nil, errors.BadRequest("identity.CREATE_INPUT", "用户名与密码必填")
	}
	if len(req.GetPassword()) < 6 {
		return nil, errors.BadRequest("identity.PASSWORD_TOO_SHORT", "密码至少 6 位")
	}
	row, err := s.repo.Register(ctx, RegisterInput{
		Username: req.GetUsername(), Password: req.GetPassword(), Email: req.GetEmail(),
	})
	if err != nil {
		return nil, errors.BadRequest("identity.CREATE_FAILED", strings.TrimPrefix(err.Error(), "identity."))
	}
	return s.enrich(ctx, []*ent.User{row})[row.ID], nil
}

// ResetUserPassword 重置密码（bcrypt 全新哈希；全部已登录态不受影响——JWT 无状态）。
func (s *AdminUserManageService) ResetUserPassword(ctx context.Context, req *adminv1.ResetUserPasswordRequest) (*emptypb.Empty, error) {
	if len(req.GetNewPassword()) < 6 {
		return nil, errors.BadRequest("identity.PASSWORD_TOO_SHORT", "密码至少 6 位")
	}
	hash, err := crypto.HashPassword(req.GetNewPassword())
	if err != nil {
		return nil, errors.InternalServer("identity.RESET_FAILED", "密码处理失败")
	}
	err = data.Client(ctx, s.repo.data).User.UpdateOneID(req.GetId()).
		SetPasswordHash(hash).
		Exec(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("identity.USER_NOT_FOUND", "用户不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("identity.RESET_FAILED", "重置密码失败")
	}
	return &emptypb.Empty{}, nil
}

// enrich 批量聚合（一页用户 5 类批查 + 等级逐条解析，杜绝 N+1 逐行查询）：
// 钱包/积分/供货账户 in 查询；订单数与消费额 group by；等级名字典批查。
func (s *AdminUserManageService) enrich(ctx context.Context, rows []*ent.User) map[uint64]*adminv1.UserItem {
	out := make(map[uint64]*adminv1.UserItem, len(rows))
	if len(rows) == 0 {
		return out
	}
	client := data.Client(ctx, s.repo.data)
	ids := make([]uint64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}

	balances := map[uint64]int64{}
	if ws, err := client.WalletAccount.Query().Where(walletaccount.UserIDIn(ids...)).All(ctx); err == nil {
		for _, w := range ws {
			balances[w.UserID] = w.Available
		}
	}
	points := map[uint64]int64{}
	if ps, err := client.PointAccount.Query().Where(pointaccount.UserIDIn(ids...)).All(ctx); err == nil {
		for _, p := range ps {
			points[p.UserID] = p.Balance
		}
	}
	suppliers := map[uint64]string{}
	if ss, err := client.SupplierAccount.Query().Where(supplieraccount.OwnerUserIDIn(ids...)).All(ctx); err == nil {
		for _, a := range ss {
			suppliers[a.OwnerUserID] = string(a.Status)
		}
	}
	// 订单聚合:两趟 group by 批量（杜绝逐用户 N+1）
	orderCount := map[uint64]int{}
	var cntRows []struct {
		UserID uint64 `json:"user_id"`
		Count  int    `json:"count"`
	}
	_ = client.Order.Query().Where(order.UserIDIn(ids...)).
		GroupBy(order.FieldUserID).
		Aggregate(ent.Count()).
		Scan(ctx, &cntRows)
	for _, r := range cntRows {
		orderCount[r.UserID] = r.Count
	}
	// 消费额 = 已支付族订单 amount 合计（排除待付款/已取消/已过期）
	spent := map[uint64]int64{}
	paidStates := []order.Status{order.StatusPaid, order.StatusFulfilling,
		order.StatusPartiallyDelivered, order.StatusDelivered, order.StatusRefundPending}
	var sumRows []struct {
		UserID uint64 `json:"user_id"`
		Sum    int64  `json:"sum"`
	}
	_ = client.Order.Query().
		Where(order.UserIDIn(ids...), order.StatusIn(paidStates...)).
		GroupBy(order.FieldUserID).
		Aggregate(ent.Sum(order.FieldTotalAmount)).
		Scan(ctx, &sumRows)
	for _, r := range sumRows {
		spent[r.UserID] = r.Sum
	}

	// 等级:RateResolver 逐用户 + 名称字典批查
	levelIDs := map[uint64]uint64{}
	for _, r := range rows {
		lid := uint64(0)
		if s.rate != nil {
			if _, l, err := s.rate.EffectiveRate(ctx, r.ID); err == nil {
				lid = l
			}
		}
		levelIDs[r.ID] = lid
	}
	levelNames := map[uint64]string{}
	distinct := map[uint64]bool{}
	for _, lid := range levelIDs {
		if lid > 0 {
			distinct[lid] = true
		}
	}
	if len(distinct) > 0 {
		lids := make([]uint64, 0, len(distinct))
		for lid := range distinct {
			lids = append(lids, lid)
		}
		if ls, err := client.MemberLevel.Query().Where(memberlevel.IDIn(lids...)).All(ctx); err == nil {
			for _, l := range ls {
				levelNames[l.ID] = l.Name
			}
		}
	}

	for _, r := range rows {
		item := &adminv1.UserItem{
			Id: r.ID, Username: r.Username, Email: r.Email,
			Status: string(r.Status), CreatedAt: r.CreatedAt.Unix(),
			BalanceCents: balances[r.ID],
			Points:       int32(points[r.ID]),
			OrderCount:   int64(orderCount[r.ID]),
			SpentCents:   spent[r.ID],
		}
		if !r.LastLoginAt.IsZero() {
			item.LastLoginAt = r.LastLoginAt.Unix()
		}
		if lid := levelIDs[r.ID]; lid > 0 {
			item.LevelId = lid
			item.LevelName = levelNames[lid]
		}
		if st, ok := suppliers[r.ID]; ok {
			item.IsSupplier = true
			item.SupplierStatus = st
		}
		out[r.ID] = item
	}
	return out
}

func validUserStatus(st string) bool {
	return st == "active" || st == "banned"
}

func normPage(page, size int32) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return int(page), int(size)
}
