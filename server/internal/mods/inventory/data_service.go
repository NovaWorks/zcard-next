package inventory

// admin API（P1-02；薄 transport——导入预览/确认/导出/列表/操作）。

import (
	"context"
	"strings"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"

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
			MaskedContent: maskContent(r.Content),
			Note:          r.Note,
		})
	}
	return reply, nil
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

// ── 工具 ──

func toImportBatch(imp *ent.CardImport) *adminv1.ImportBatch {
	return &adminv1.ImportBatch{
		Id: imp.ID, ProductId: imp.ProductID,
		Total: imp.Total, Imported: imp.Imported,
		Skipped: imp.Skipped, Failed: imp.Failed,
		Status: string(imp.Status),
	}
}

// maskContent 掩码（尾 4 位；密文场景先解密再掩码——此处简化为 ID 标识）。
func maskContent(content []byte) string {
	if len(content) < 4 {
		return "****"
	}
	return "****" + string(content[len(content)-4:])
}

var _ = strings.TrimSpace
