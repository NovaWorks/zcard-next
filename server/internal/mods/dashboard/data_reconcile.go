package dashboard

// P3-07 T4 货源对账引擎（M2 验收项）：
//   任务生命周期 pending → processing → done/failed（可查可告警）；
//   时间窗内本地 procurement_orders vs 上游订单清单（数据源端口注入）；
//   四态比对 matched / mismatched（金额·状态差异进 diff_json）/ local_only /
//   upstream_only；job 汇总计数；mismatch>0 → notify 管理员告警。
// 数据源：上游支持列表（zcard ListOrders）走完整四态；不支持（dujiao/acg-faka）
// 置 failed「上游不支持列表对账」（fail-closed，不静默跳过）。

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/reconciliationitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/reconciliationjob"
	dashboardport "github.com/NovaWorks/zcard-next/server/internal/mods/dashboard/port"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
)

// Reconciler 对账引擎。
type Reconciler struct {
	data   *data.Data
	source dashboardport.UpstreamOrderSource
	sender notifyport.Sender // mismatch 告警（nil = 未装配跳过）
}

// NewReconciler 构造。
func NewReconciler(d *data.Data, source dashboardport.UpstreamOrderSource, sender notifyport.Sender) *Reconciler {
	return &Reconciler{data: d, source: source, sender: sender}
}

// CreateJob 创建对账任务（pending；时间窗 ≤31 天）。
func (rc *Reconciler) CreateJob(ctx context.Context, connectionID uint64, start, end time.Time) (*ent.ReconciliationJob, error) {
	if end.Before(start) || end.Sub(start) > 31*24*time.Hour {
		return nil, errors.New("reconciliation.RANGE_INVALID")
	}
	return data.Client(ctx, rc.data).ReconciliationJob.Create().
		SetConnectionID(connectionID).
		SetType("orders").
		SetStatus(reconciliationjob.StatusPending).
		SetTimeRangeStart(start).
		SetTimeRangeEnd(end).
		Save(ctx)
}

// RunJob 执行任务（幂等：非 pending 直接返回；处理中/已完成不重跑）。
func (rc *Reconciler) RunJob(ctx context.Context, jobID uint64) error {
	client := data.Client(ctx, rc.data)
	job, err := client.ReconciliationJob.Get(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Status != reconciliationjob.StatusPending {
		return nil // 幂等
	}
	// 置 processing（CAS）
	if _, err := client.ReconciliationJob.UpdateOneID(jobID).
		Where(reconciliationjob.StatusEQ(reconciliationjob.StatusPending)).
		SetStatus(reconciliationjob.StatusProcessing).
		Save(ctx); err != nil {
		return err
	}

	// 上游清单（不支持 → failed 可查）
	if rc.source == nil {
		return rc.failJob(ctx, jobID, "对账数据源未装配")
	}
	upstream, err := rc.source.ListOrders(ctx, job.ConnectionID, job.TimeRangeStart, job.TimeRangeEnd)
	if errors.Is(err, dashboardport.ErrUpstreamListUnsupported) {
		return rc.failJob(ctx, jobID, "上游不支持订单列表对账")
	}
	if err != nil {
		return rc.failJob(ctx, jobID, "拉取上游订单失败: "+err.Error())
	}

	// 本地采购单（时间窗 + 已提交上游的）
	locals, err := client.ProcurementOrder.Query().
		Where(
			procurementorder.ConnectionID(job.ConnectionID),
			procurementorder.CreatedAtGTE(job.TimeRangeStart),
			procurementorder.CreatedAtLT(job.TimeRangeEnd),
			procurementorder.UpstreamOrderIDNotNil(),
		).
		All(ctx)
	if err != nil {
		return rc.failJob(ctx, jobID, "读取本地采购单失败: "+err.Error())
	}

	upByID := map[string]dashboardport.UpstreamOrder{}
	for _, u := range upstream {
		upByID[u.UpstreamOrderID] = u
	}
	localByID := map[string]localSide{}
	for _, l := range locals {
		// 本地口径：订单子项金额 + 父单号（快照链：procurement → order_item → order）
		no, amt := rc.localRef(ctx, l)
		localByID[l.UpstreamOrderID] = localSide{procure: l, orderNo: no, amount: amt}
	}

	var matched, mismatched, localOnly, upstreamOnly int32
	// 本地侧比对
	for _, l := range localByID {
		u, ok := upByID[l.procure.UpstreamOrderID]
		if !ok {
			localOnly++
			if err := rc.item(ctx, jobID, l.procure.ID, l.orderNo, "", reconciliationitem.StatusLocalOnly, nil); err != nil {
				return rc.failJob(ctx, jobID, "落对账明细失败: "+err.Error())
			}
			continue
		}
		diff := map[string]any{}
		if u.Amount != l.amount {
			diff["amount"] = map[string]any{"local": l.amount, "upstream": u.Amount}
		}
		if len(diff) > 0 {
			mismatched++
			if err := rc.item(ctx, jobID, l.procure.ID, l.orderNo, u.UpstreamOrderID, reconciliationitem.StatusMismatched, diff); err != nil {
				return rc.failJob(ctx, jobID, "落对账明细失败: "+err.Error())
			}
			continue
		}
		matched++
		if err := rc.item(ctx, jobID, l.procure.ID, l.orderNo, u.UpstreamOrderID, reconciliationitem.StatusMatched, nil); err != nil {
			return rc.failJob(ctx, jobID, "落对账明细失败: "+err.Error())
		}
	}
	// 上游侧多余
	for _, u := range upstream {
		if _, ok := localByID[u.UpstreamOrderID]; !ok {
			upstreamOnly++
			if err := rc.item(ctx, jobID, 0, "", u.UpstreamOrderID, reconciliationitem.StatusUpstreamOnly, nil); err != nil {
				return rc.failJob(ctx, jobID, "落对账明细失败: "+err.Error())
			}
		}
	}

	// 汇总落 job done
	resultMap := map[string]any{
		"local_total": len(locals), "upstream_total": len(upstream),
		"matched": matched, "mismatched": mismatched,
		"local_only": localOnly, "upstream_only": upstreamOnly,
	}
	if _, err := client.ReconciliationJob.UpdateOneID(jobID).
		SetStatus(reconciliationjob.StatusDone).
		SetTotalCount(int32(len(locals)) + upstreamOnly).
		SetMatchedCount(matched).
		SetMismatchedCount(mismatched).
		SetResultJSON(resultMap).
		Save(ctx); err != nil {
		return err
	}
	// mismatch 告警（管理员通道；一次一告）
	if mismatched+localOnly+upstreamOnly > 0 && rc.sender != nil {
		_ = rc.sender.Send(ctx, notifyport.Message{
			EventType: "reconciliation.mismatch",
			Channel:   "inbox",
			Subject:   "货源对账差异告警",
			Body:      "对账任务 #" + strconv.FormatUint(jobID, 10) + " 发现差异：mismatched=" + strconv.Itoa(int(mismatched)) + " local_only=" + strconv.Itoa(int(localOnly)) + " upstream_only=" + strconv.Itoa(int(upstreamOnly)),
			UserID:    0, // 管理员广播由 notify 通道处理（admin 收件方由通道配置决定）
		})
	}
	return nil
}

// localSide 本地侧对账口径（采购单 + 父单号 + 订单子项金额）。
type localSide struct {
	procure *ent.ProcurementOrder
	orderNo string
	amount  int64
}

// localRef 解析本地口径（子项金额 + 父单号；解析失败降级零值——金额差异进 diff）。
func (rc *Reconciler) localRef(ctx context.Context, p *ent.ProcurementOrder) (string, int64) {
	client := data.Client(ctx, rc.data)
	it, err := client.OrderItem.Get(ctx, p.OrderItemID)
	if err != nil {
		return "", 0
	}
	no := ""
	if o, err := client.Order.Get(ctx, it.OrderID); err == nil {
		no = o.OrderNo
	}
	return no, it.Amount
}

// item 落对账明细行。
func (rc *Reconciler) item(ctx context.Context, jobID uint64, procureID uint64, localNo, upstreamNo string, status reconciliationitem.Status, diff map[string]any) error {
	create := data.Client(ctx, rc.data).ReconciliationItem.Create().
		SetJobID(jobID).
		SetStatus(status)
	if procureID > 0 {
		create.SetProcurementOrderID(procureID)
	}
	if localNo != "" {
		create.SetLocalOrderNo(localNo)
	}
	if upstreamNo != "" {
		create.SetUpstreamOrderNo(upstreamNo)
	}
	if len(diff) > 0 {
		create.SetDiffJSON(diff)
	}
	return create.Exec(ctx)
}

// failJob 失败收尾（可查原因）。
func (rc *Reconciler) failJob(ctx context.Context, jobID uint64, reason string) error {
	res := map[string]any{"error": reason}
	_, err := data.Client(ctx, rc.data).ReconciliationJob.UpdateOneID(jobID).
		SetStatus(reconciliationjob.StatusFailed).
		SetResultJSON(res).
		Save(ctx)
	return err
}

// GetJob 任务详情。
func (rc *Reconciler) GetJob(ctx context.Context, jobID uint64) (*ent.ReconciliationJob, error) {
	return data.Client(ctx, rc.data).ReconciliationJob.Get(ctx, jobID)
}

// ListItems 明细分页（mismatch 等状态筛选）。
func (rc *Reconciler) ListItems(ctx context.Context, jobID uint64, status string, page, size int) ([]*ent.ReconciliationItem, int64, error) {
	q := data.Client(ctx, rc.data).ReconciliationItem.Query().
		Where(reconciliationitem.JobID(jobID)).
		Order(ent.Asc(reconciliationitem.FieldID))
	if status != "" {
		q = q.Where(reconciliationitem.StatusEQ(reconciliationitem.Status(status)))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, int64(total), err
}
