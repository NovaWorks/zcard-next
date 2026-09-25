package supply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyimportitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplysynctask"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"google.golang.org/protobuf/proto"
)

type importPayload struct {
	ActiveCodes []string          `json:"active_codes,omitempty"`
	Tenant      uint64            `json:"tenant"`
	Fingerprint string            `json:"fingerprint"`
	Mode        string            `json:"mode"`
	Percent     float64           `json:"percent"`
	Amount      int64             `json:"amount"`
	Categories  map[string]uint64 `json:"categories"`
}

func importFingerprint(c *ent.SupplyConnection) string {
	// Never persist plaintext credentials in task payloads.
	b, _ := json.Marshal([]any{previewIdentity(c), c.ExchangeRate, c.PriceMarkupPercent, c.PriceMarkupAmount, c.PriceRoundingMode, c.AutoSyncPrice, c.Status, categoryMapFromSettings(c.Settings)})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func productRevision(p *ent.Product) string {
	b, _ := json.Marshal([]any{p.ID, p.Name, p.Price, p.FactoryPrice, p.CategoryID, p.Status, p.Cover, p.Description, p.Images, p.MemberPrice, p.CoverProtected, p.DescriptionProtected, p.IsLocked, p.LockVersion})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func importRequestIdentity(req *adminv1.ImportProductsRequest) (string, string, error) {
	if len(req.Codes) == 0 || len(req.Codes) > 5000 {
		return "", "", fmt.Errorf("请选择 1–5000 件商品")
	}
	normalized := proto.Clone(req).(*adminv1.ImportProductsRequest)
	normalized.RequestKey = ""
	normalized.Codes = append([]string{}, req.Codes...)
	sort.Strings(normalized.Codes)
	normalized.Codes = compactCodes(normalized.Codes)
	for _, code := range normalized.Codes {
		if code == "" || len(code) > 128 {
			return "", "", fmt.Errorf("商品标识无效")
		}
	}
	sort.Slice(normalized.CategoryDrafts, func(i, j int) bool {
		return normalized.CategoryDrafts[i].GetUpstreamCode() < normalized.CategoryDrafts[j].GetUpstreamCode()
	})
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(normalized)
	if err != nil {
		return "", "", err
	}
	h := sha256.Sum256(b)
	hash := hex.EncodeToString(h[:])
	key := req.RequestKey
	if key == "" {
		key = hash
	} // backward-compatible idempotency for older clients
	if len(key) > 64 {
		return "", "", fmt.Errorf("提交标识过长")
	}
	return key, hash, nil
}
func compactCodes(codes []string) []string {
	out := make([]string, 0, len(codes))
	seen := map[string]bool{}
	for _, c := range codes {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}
func (s *AdminSupplyService) existingImport(ctx context.Context, connectionID uint64, key, hash string) (*ent.SupplySyncTask, error) {
	t, err := s.repo.entClient(ctx).SupplySyncTask.Query().Where(supplysynctask.ConnectionID(connectionID), supplysynctask.RequestKeyEQ(key)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t.RequestHash != hash {
		return nil, fmt.Errorf("同一次提交的参数已变化，请重新发起导入")
	}
	if _, err := readImportPayload(ctx, t, true); err != nil {
		return nil, err
	}
	return t, nil
}
func readImportPayload(ctx context.Context, t *ent.SupplySyncTask, checkTenant bool) (*importPayload, error) {
	var p importPayload
	if t.Scope != ScopeImport || json.Unmarshal(t.ImportPayload, &p) != nil {
		return nil, fmt.Errorf("不是有效的商品导入任务")
	}
	if checkTenant && p.Tenant != tenancy.FromContext(ctx).SubsiteID {
		return nil, ErrNotFound
	}
	return &p, nil
}
func (s *AdminSupplyService) ImportProducts(ctx context.Context, req *adminv1.ImportProductsRequest) (*adminv1.ImportProductsReply, error) {
	key, hash, err := importRequestIdentity(req)
	if err != nil {
		return nil, err
	}
	if t, err := s.existingImport(ctx, req.ConnectionId, key, hash); err != nil {
		return nil, err
	} else if t != nil {
		return s.importAccepted(ctx, t)
	}
	conn, err := s.repo.GetConnection(ctx, req.ConnectionId)
	if err != nil {
		return nil, err
	}
	entry, err := s.loadPreview(ctx, conn.ID)
	if err != nil {
		return nil, err
	}
	if entry.identity != previewIdentity(conn) {
		return nil, fmt.Errorf("货源账号已变化，请刷新目录")
	}
	task, err := s.createImportTask(ctx, conn, req, entry.byCode, key, hash)
	if err != nil {
		return nil, err
	}
	// Recovery also dispatches pending tasks, so a failed wake-up never loses work.
	if err := s.sync.StartTask(context.WithoutCancel(ctx), task.ID); err != nil {
		s.sync.log.Warn("supply.import.wakeup_failed", "task_id", task.ID, "err", err)
	}
	previewCache.Lock()
	delete(previewCache.m, conn.ID)
	previewCache.Unlock()
	return s.importAccepted(ctx, task)
}
func (s *AdminSupplyService) importAccepted(ctx context.Context, t *ent.SupplySyncTask) (*adminv1.ImportProductsReply, error) {
	payload, err := readImportPayload(ctx, t, true)
	if err != nil {
		return nil, err
	}
	task, err := s.importTaskProto(ctx, t)
	if err != nil {
		return nil, err
	}
	return &adminv1.ImportProductsReply{Task: task, CategoryMap: payload.Categories}, nil
}

// Selection, category mapping and durable work are one transaction. No upstream
// HTTP calls are made here; the preview snapshot survives process/cache expiry.
func (s *AdminSupplyService) createImportTask(ctx context.Context, conn *ent.SupplyConnection, req *adminv1.ImportProductsRequest, products map[string]adapter.Product, key, hash string) (*ent.SupplySyncTask, error) {
	if string(conn.Status) != "active" {
		return nil, fmt.Errorf("货源已停用，请启用后再导入")
	}
	mode, percent, amount := req.PricingMode, req.MarkupPercent, req.MarkupAmountCents
	if mode == "" {
		mode = PriceModeChannel
		if def, ok := conn.Settings["import_pricing"].(map[string]any); ok {
			if v, _ := def["mode"].(string); v != "" {
				mode = v
			}
			percent, _ = def["markup_percent"].(float64)
			amount = toInt64(def["markup_amount_cents"])
		}
	}
	switch mode {
	case PriceModeChannel, PriceModePercent, PriceModeFixed, PriceModeEqual, PriceModePending:
	default:
		return nil, fmt.Errorf("无效的导入定价策略")
	}
	if err := validatePricing(conn.ExchangeRate, percent, amount, string(conn.PriceRoundingMode)); err != nil {
		return nil, err
	}
	codes := compactCodes(req.Codes)
	for _, code := range codes {
		if _, ok := products[code]; !ok {
			return nil, fmt.Errorf("商品 %s 已不在目录，请刷新后重试", code)
		}
	}
	var task *ent.SupplySyncTask
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := s.repo.entClient(ctx)
		if err := c.SupplyConnection.UpdateOneID(conn.ID).AddRetryMax(0).Exec(ctx); err != nil {
			return err
		}
		if t, err := s.existingImport(ctx, conn.ID, key, hash); err != nil {
			return err
		} else if t != nil {
			task = t
			return nil
		}
		current, err := s.repo.GetConnection(ctx, conn.ID)
		if err != nil {
			return err
		}
		if importFingerprint(current) != importFingerprint(conn) {
			return fmt.Errorf("货源配置已变化，请刷新后重试")
		}
		cats, err := s.saveImportCategories(ctx, req, products, mode, percent, amount)
		if err != nil {
			return err
		}
		current, err = s.repo.GetConnection(ctx, conn.ID)
		if err != nil {
			return err
		}
		payload := importPayload{Tenant: tenancy.FromContext(ctx).SubsiteID, Fingerprint: importFingerprint(current), Mode: mode, Percent: percent, Amount: amount, Categories: cats}
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		task, err = c.SupplySyncTask.Create().SetConnectionID(conn.ID).SetMode("selected").SetScope(ScopeImport).SetRequestKey(key).SetRequestHash(hash).SetImportPayload(raw).SetTotalCount(int32(len(codes))).Save(ctx)
		if err != nil {
			return err
		}
		locals, err := c.Product.Query().Where(product.UpstreamSourceID(conn.ID), product.UpstreamProductCodeIn(codes...), product.SubsiteID(payload.Tenant), product.StatusGTE(0)).All(ctx)
		if err != nil {
			return err
		}
		byCode := map[string]*ent.Product{}
		for _, p := range locals {
			byCode[p.UpstreamProductCode] = p
		}
		for start := 0; start < len(codes); start += 50 {
			end := start + 50
			if end > len(codes) {
				end = len(codes)
			}
			builders := make([]*ent.SupplyImportItemCreate, 0, end-start)
			for _, code := range codes[start:end] {
				p := products[code]
				if conn.Driver == "acg_faka" {
					p.Stock = -2
				}
				raw, err := json.Marshal(p)
				if err != nil {
					return err
				}
				b := c.SupplyImportItem.Create().SetTaskID(task.ID).SetCode(code).SetName(p.Name).SetSnapshot(raw)
				if local := byCode[code]; local != nil {
					b.SetLocalProductID(local.ID).SetLocalRevision(productRevision(local))
				}
				builders = append(builders, b)
			}
			if err := c.SupplyImportItem.CreateBulk(builders...).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	return task, err
}

func (s *AdminSupplyService) importTaskProto(ctx context.Context, t *ent.SupplySyncTask) (*adminv1.SupplySyncTask, error) {
	out := toProtoTask(t)
	if t.Scope != ScopeImport {
		return out, nil
	}
	if _, err := readImportPayload(ctx, t, true); err != nil {
		return nil, err
	}
	rows, err := s.repo.entClient(ctx).SupplyImportItem.Query().Where(supplyimportitem.TaskID(t.ID)).Select(supplyimportitem.FieldState, supplyimportitem.FieldStage, supplyimportitem.FieldSaved, supplyimportitem.FieldCreated, supplyimportitem.FieldNextAttemptAt).All(ctx)
	if err != nil {
		return nil, err
	}
	out.Processed, out.Created, out.Updated, out.ManualSkipped = 0, 0, 0, 0
	for _, r := range rows {
		if r.Saved {
			if r.Created {
				out.Created++
			} else {
				out.Updated++
			}
		}
		if r.State == "done" || r.State == "failed" || r.State == "skipped" {
			out.Processed++
		}
		if r.Saved && r.Stage == "stock" && r.State != "done" {
			out.StockPendingCount++
			if r.State == "failed" {
				out.StockFailedCount++
			}
		}
		if r.Saved {
			continue
		} // stock issues are a subset of saved products
		switch r.State {
		case "skipped":
			out.ManualSkipped++
		case "failed":
			out.FailedCount++
		case "retry":
			out.RetryingCount++
		case "pending":
			out.PendingCount++
		}
	}
	for _, r := range rows {
		if r.State == "retry" && r.NextAttemptAt > 0 && (out.NextRetryAt == 0 || r.NextAttemptAt < out.NextRetryAt) {
			out.NextRetryAt = r.NextAttemptAt
		}
	}
	return out, nil
}
func (s *AdminSupplyService) ListImportItems(ctx context.Context, req *adminv1.ListImportItemsRequest) (*adminv1.ListImportItemsReply, error) {
	t, err := s.repo.GetSyncTask(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if _, err := readImportPayload(ctx, t, true); err != nil {
		return nil, err
	}
	q := s.repo.entClient(ctx).SupplyImportItem.Query().Where(supplyimportitem.TaskID(t.ID))
	if req.ProblemsOnly {
		q.Where(supplyimportitem.Or(supplyimportitem.StateIn("retry", "failed", "skipped"), supplyimportitem.And(supplyimportitem.StageEQ("stock"), supplyimportitem.StateNEQ("done"))))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, err
	}
	page, size := pageParams(req.Page, req.PageSize)
	rows, err := q.Order(supplyimportitem.ByID()).Offset((page - 1) * size).Limit(size).All(ctx)
	if err != nil {
		return nil, err
	}
	out := &adminv1.ListImportItemsReply{Total: int64(total)}
	for _, r := range rows {
		out.Items = append(out.Items, &adminv1.SupplyImportItem{Code: r.Code, Name: r.Name, State: r.State, Stage: r.Stage, Saved: r.Saved, Created: r.Created, Attempts: int32(r.Attempts), NextRetryAt: r.NextAttemptAt, ErrorCode: r.ErrorCode, ErrorSummary: r.ErrorSummary})
	}
	return out, nil
}

func (s *AdminSupplyService) RetryImportTask(ctx context.Context, req *adminv1.RetryImportTaskRequest) (*adminv1.SupplySyncTask, error) {
	task, err := s.prepareImportRetry(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.sync.StartTask(context.WithoutCancel(ctx), task.ID); err != nil {
		s.sync.log.Warn("supply.import.retry_wakeup_failed", "task_id", task.ID)
	}
	return s.importTaskProto(ctx, task)
}
func (s *AdminSupplyService) prepareImportRetry(ctx context.Context, req *adminv1.RetryImportTaskRequest) (*ent.SupplySyncTask, error) {
	var task *ent.SupplySyncTask
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		t, err := s.repo.GetSyncTask(ctx, req.Id)
		if err != nil {
			return err
		}
		payload, err := readImportPayload(ctx, t, true)
		if err != nil {
			return err
		}
		c := s.repo.entClient(ctx)
		if err := c.SupplyConnection.UpdateOneID(t.ConnectionID).AddRetryMax(0).Exec(ctx); err != nil {
			return err
		}
		conn, err := s.repo.GetConnection(ctx, t.ConnectionID)
		if err != nil {
			return err
		}
		if string(conn.Status) != "active" {
			return fmt.Errorf("货源已停用，请启用后再继续任务")
		}
		if conn.SyncLeaseUntil > time.Now().Unix() {
			return fmt.Errorf("货源任务尚未停止，请稍后再试")
		}
		t, err = s.repo.GetSyncTask(ctx, req.Id)
		if err != nil {
			return err
		}
		if t.Status == supplysynctask.StatusPending || t.Status == supplysynctask.StatusProcessing {
			task = t
			return nil
		}
		// Never silently change the meaning of a queued pricing/category selection.
		if payload.Fingerprint != importFingerprint(conn) {
			if !req.ConfirmConfig {
				return fmt.Errorf("货源配置已变化，请确认使用当前配置后继续")
			}
			payload.Fingerprint = importFingerprint(conn)
			payload.Categories = categoryMapFromSettings(conn.Settings)
			raw, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			if err := c.SupplySyncTask.UpdateOneID(t.ID).SetImportPayload(raw).Exec(ctx); err != nil {
				return err
			}
		}
		q := c.SupplyImportItem.Query().Where(supplyimportitem.TaskID(t.ID))
		switch req.Scope {
		case "stock":
			q.Where(supplyimportitem.SavedEQ(true), supplyimportitem.StageEQ("stock"), supplyimportitem.StateNEQ("done"), supplyimportitem.StateNEQ("skipped"))
		case "failed":
			q.Where(supplyimportitem.StateEQ("failed"))
		case "remaining", "":
			q.Where(supplyimportitem.StateIn("pending", "retry", "failed"))
		default:
			return fmt.Errorf("重试范围无效")
		}
		targets, err := q.Select(supplyimportitem.FieldCode).All(ctx)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			return fmt.Errorf("没有需要重试的商品")
		}
		payload.ActiveCodes = make([]string, 0, len(targets))
		for _, item := range targets {
			payload.ActiveCodes = append(payload.ActiveCodes, item.Code)
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if err := c.SupplySyncTask.UpdateOneID(t.ID).SetImportPayload(raw).Exec(ctx); err != nil {
			return err
		}
		if err := c.SupplyImportItem.Update().Where(supplyimportitem.TaskID(t.ID), supplyimportitem.CodeIn(payload.ActiveCodes...)).SetState("pending").SetAttempts(0).SetNextAttemptAt(0).SetErrorCode("").SetErrorSummary("").Exec(ctx); err != nil {
			return err
		}

		task, err = c.SupplySyncTask.UpdateOneID(t.ID).SetStatus(supplysynctask.StatusPending).ClearCancelRequestedAt().ClearFinishedAt().ClearErrorCode().ClearErrorContext().Save(ctx)
		return err
	})
	return task, err
}

var errImportChanged = errors.New("商品已被人工修改，已跳过以保留当前设置")
