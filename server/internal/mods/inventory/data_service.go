package inventory

// admin API（P1-02；薄 transport——导入预览/确认/导出/列表/操作）。

import (
	"context"
	"strings"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/securityauditlog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminInventoryService 库存管理服务。
type AdminInventoryService struct {
	adminv1.UnimplementedAdminInventoryServiceServer
	repo *CardRepoImpl
	data *data.Data
}

// NewAdminInventoryService 构造。
func NewAdminInventoryService(repo *CardRepoImpl, d *data.Data) *AdminInventoryService {
	return &AdminInventoryService{repo: repo, data: d}
}

// ImportPreview 上传预览。
func (s *AdminInventoryService) ImportPreview(ctx context.Context, req *adminv1.ImportPreviewRequest) (*adminv1.ImportPreviewReply, error) {
	if req.GetProductId() == 0 || len(req.GetLines()) == 0 {
		return nil, errors.BadRequest("inventory.INVALID_INPUT", "商品 ID 与卡密行必填")
	}
	p, err := s.repo.ParseLines(ctx, ImportInput{
		ProductID: req.GetProductId(),
		Lines:     req.GetLines(),
		Dedup:     req.GetDedup(),
	})
	if err != nil {
		return nil, errors.InternalServer("inventory.PREVIEW_FAILED", "预览失败")
	}
	return &adminv1.ImportPreviewReply{
		Total: int32(p.Total), DupInFile: int32(p.DupInFile), DupInDb: int32(p.DupInDB),
		Invalid: int32(p.Invalid), Sample: p.Sample, IsPremium: p.IsPremium,
	}, nil
}

// ImportConfirm 确认导入。
func (s *AdminInventoryService) ImportConfirm(ctx context.Context, req *adminv1.ImportConfirmRequest) (*adminv1.ImportBatch, error) {
	if req.GetProductId() == 0 || len(req.GetLines()) == 0 {
		return nil, errors.BadRequest("inventory.INVALID_INPUT", "商品 ID 与卡密行必填")
	}
	imp, err := s.repo.ImportConfirm(ctx, ImportInput{
		ProductID: req.GetProductId(),
		Lines:     req.GetLines(),
		Dedup:     req.GetDedup(),
	})
	if err != nil {
		return nil, errors.InternalServer("inventory.IMPORT_FAILED", "导入失败")
	}
	return toImportBatch(imp), nil
}

// ListImports 批次列表。
func (s *AdminInventoryService) ListImports(ctx context.Context, req *adminv1.ListImportsRequest) (*adminv1.ListImportsReply, error) {
	rows, err := s.repo.ListImports(ctx, req.GetProductId())
	if err != nil {
		return nil, errors.InternalServer("inventory.LIST_FAILED", "读取批次失败")
	}
	reply := &adminv1.ListImportsReply{}
	for _, r := range rows {
		reply.Batches = append(reply.Batches, toImportBatch(r))
	}
	return reply, nil
}

// CancelImport 撤销批次。
func (s *AdminInventoryService) CancelImport(ctx context.Context, req *adminv1.CancelImportRequest) (*emptypb.Empty, error) {
	if err := s.repo.CancelImport(ctx, req.GetId()); err != nil {
		return nil, errors.InternalServer("inventory.CANCEL_FAILED", "撤销失败")
	}
	return &emptypb.Empty{}, nil
}

// ListCards 卡密列表（掩码默认）。
func (s *AdminInventoryService) ListCards(ctx context.Context, req *adminv1.ListCardsRequest) (*adminv1.ListCardsReply, error) {
	page := int(req.GetPage())
	size := int(req.GetPageSize())
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	q := data.Client(ctx, s.data).Card.Query().
		Where(card.ProductID(req.GetProductId()))
	if req.GetStatus() != "" {
		q = q.Where(card.StatusEQ(card.Status(req.GetStatus())))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, errors.InternalServer("inventory.LIST_FAILED", "读取卡密失败")
	}
	rows, err := q.Clone().Offset((page - 1) * size).Limit(size).Order(ent.Asc(card.FieldID)).All(ctx)
	if err != nil {
		return nil, errors.InternalServer("inventory.LIST_FAILED", "读取卡密失败")
	}
	reply := &adminv1.ListCardsReply{Total: int64(total)}
	for _, r := range rows {
		reply.Cards = append(reply.Cards, &adminv1.CardInfo{
			Id: r.ID, ProductId: r.ProductID, Status: string(r.Status),
			MaskedContent: s.maskPlain(r),
			Note:          r.Note,
		})
	}
	return reply, nil
}

// maskPlain 管理员默认掩码（尾 4 位明文；解密失败降级为 ****，绝不 500——铁律 5/13）。
func (s *AdminInventoryService) maskPlain(r *ent.Card) string {
	if s.repo.Cipher == nil {
		return "****"
	}
	plain, err := s.repo.Cipher.Open(r.Content, r.ProductID, r.SubsiteID)
	if err != nil {
		return "****"
	}
	return maskContent(plain)
}

// ExportCards 导出（card:export 权限已在 middleware 校验；此处内容解密）。
func (s *AdminInventoryService) ExportCards(ctx context.Context, req *adminv1.ExportCardsRequest) (*adminv1.ExportCardsReply, error) {
	lines, err := s.repo.ExportCards(ctx, req.GetProductId())
	if err != nil {
		return nil, errors.InternalServer("inventory.EXPORT_FAILED", "导出失败")
	}
	return &adminv1.ExportCardsReply{Lines: lines}, nil
}

// ToggleCard 禁用/启用。
func (s *AdminInventoryService) ToggleCard(ctx context.Context, req *adminv1.ToggleCardRequest) (*emptypb.Empty, error) {
	status := card.StatusDisabled
	if req.GetEnable() {
		status = card.StatusAvailable
	}
	_, err := data.Client(ctx, s.data).Card.UpdateOneID(req.GetId()).SetStatus(status).Save(ctx)
	if err != nil {
		return nil, errors.NotFound("inventory.CARD_NOT_FOUND", "卡密不存在")
	}
	return &emptypb.Empty{}, nil
}

// ViewCardContent 查看完整卡密（card:view_content 权限已在 middleware 校验；安全审计）。
func (s *AdminInventoryService) ViewCardContent(ctx context.Context, req *adminv1.ViewCardContentRequest) (*adminv1.ViewCardContentReply, error) {
	c, err := data.Client(ctx, s.data).Card.Get(ctx, req.GetId())
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("inventory.CARD_NOT_FOUND", "卡密不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("inventory.GET_FAILED", "查询失败")
	}
	if s.repo.Cipher == nil {
		return nil, errors.InternalServer("inventory.CIPHER_MISSING", "卡密密钥未配置")
	}
	plain, err := s.repo.Cipher.Open(c.Content, c.ProductID, c.SubsiteID)
	if err != nil {
		return nil, errors.InternalServer("inventory.DECRYPT_FAILED", "解密失败（密钥可能已轮换）")
	}

	// 安全审计：记录解密事件（明文绝不入审计，只记 card_id / actor）
	claims := identity.ClaimsFromContext(ctx)
	actorID := uint64(0)
	if claims != nil {
		actorID = claims.Subject
	}
	_, _ = data.Client(ctx, s.data).SecurityAuditLog.Create().
		SetActorType(securityauditlog.ActorTypeAdmin).
		SetNillableActorID(nilOr(actorID)).
		SetAction("card.view_content").
		SetMetadata(map[string]any{"card_id": c.ID, "product_id": c.ProductID}).
		Save(ctx)

	return &adminv1.ViewCardContentReply{Id: c.ID, Content: plain}, nil
}

// ListPremiumCards 靓号列表（number_hash 命中 + 可用卡；card:premium 权限已在 middleware 校验）。
func (s *AdminInventoryService) ListPremiumCards(ctx context.Context, req *adminv1.ListPremiumCardsRequest) (*adminv1.ListPremiumCardsReply, error) {
	q := data.Client(ctx, s.data).Card.Query().
		Where(
			card.ProductID(req.GetProductId()),
			card.StatusEQ(card.StatusAvailable),
			card.NumberHashNotNil(),
		)
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, errors.InternalServer("inventory.PREMIUM_LIST_FAILED", "读取靓号失败")
	}
	rows, err := q.Clone().Order(ent.Asc(card.FieldID)).Limit(500).All(ctx)
	if err != nil {
		return nil, errors.InternalServer("inventory.PREMIUM_LIST_FAILED", "读取靓号失败")
	}
	reply := &adminv1.ListPremiumCardsReply{Total: int64(total)}
	for _, r := range rows {
		reply.Cards = append(reply.Cards, &adminv1.PremiumCardInfo{
			Id: r.ID, ProductId: r.ProductID, MaskedContent: s.maskPlain(r),
			DraftPremium: r.DraftPremium, DraftCost: r.DraftCost,
			PriceCents: r.Price, Status: string(r.Status),
		})
	}
	return reply, nil
}

func nilOr(v uint64) *uint64 {
	if v == 0 {
		return nil
	}
	return &v
}

// ── 工具 ──

func toImportBatch(imp *ent.CardImport) *adminv1.ImportBatch {
	return &adminv1.ImportBatch{
		Id: imp.ID, ProductId: imp.ProductID,
		Total: imp.Total, Imported: imp.Imported,
		Skipped: imp.Skipped, Failed: imp.Failed,
		Status: string(imp.Status),
	}
}

// maskContent 掩码明文（尾 4 位；不足 4 位返回 ****）。
func maskContent(plain string) string {
	if len(plain) <= 4 {
		return "****"
	}
	return "****" + plain[len(plain)-4:]
}

var _ = strings.TrimSpace
