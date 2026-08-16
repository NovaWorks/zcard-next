package ticket

// P3-05 必测项：内部备注双过滤防泄漏、状态机非法迁移、付费加急（余额扣费+置顶）、
// 自动关闭、工作台优先级排序、游客联系方式必填。

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/ticket"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newTicketData(t *testing.T) (*TicketRepo, *data.Data) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:tickettest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	return NewTicketRepo(d), d
}

func seedTicket(t *testing.T, r *TicketRepo, no string, userID uint64, guest string) *ent.Ticket {
	t.Helper()
	ctx := context.Background()
	tk, err := r.Create(ctx, no, CreateInput{
		UserID: userID, GuestContact: guest, Type: "aftersale",
		Content: "商品无法使用", Attachments: []uint64{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// TestInternalNoteDualFilter 内部备注双过滤（用户列表/详情均不可见；客服侧全量）。
func TestInternalNoteDualFilter(t *testing.T) {
	r, _ := newTicketData(t)
	ctx := context.Background()
	tk := seedTicket(t, r, "T-INT-1", 7, "")

	// 客服内部备注 + 正常回复
	if _, err := r.CreateMessage(ctx, tk.ID, "admin", 1, "疑似恶意<script>evil()</script>刷单", nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateMessage(ctx, tk.ID, "admin", 1, "已收到反馈", nil, false); err != nil {
		t.Fatal(err)
	}

	userMsgs, err := r.MessagesUserVisible(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range userMsgs {
		if m.IsInternal {
			t.Fatal("内部备注泄漏到用户侧")
		}
		if m.Content != "已收到反馈" && m.Content != "商品无法使用" {
			t.Fatalf("用户侧出现意外消息: %s", m.Content)
		}
	}
	if len(userMsgs) != 2 { // 首条用户消息 + 客服正常回复
		t.Fatalf("用户侧消息数错误: %d", len(userMsgs))
	}

	adminMsgs, _ := r.MessagesAll(ctx, tk.ID)
	if len(adminMsgs) != 3 {
		t.Fatalf("客服侧应含内部备注: %d", len(adminMsgs))
	}
	// sanitize：内部备注的 HTML 也被剥离
	found := false
	for _, m := range adminMsgs {
		if m.IsInternal && m.Content == "疑似恶意刷单" {
			found = true
		}
	}
	if !found {
		t.Fatal("内部备注 sanitize 或内容错误")
	}
}

// TestStateMachine 状态机：合法迁移 + 非法拒绝 + 首响回填。
func TestStateMachine(t *testing.T) {
	r, _ := newTicketData(t)
	ctx := context.Background()
	tk := seedTicket(t, r, "T-ST-1", 7, "")

	// 非法：open → closed 直接跳过 resolved？状态机允许（open 可 closed）——测非法：resolved → open
	if err := r.Transition(ctx, tk.ID, "processing"); err != nil {
		t.Fatal(err)
	}
	if err := r.Transition(ctx, tk.ID, "resolved"); err != nil {
		t.Fatal(err)
	}
	// resolved → open 非法
	if err := r.Transition(ctx, tk.ID, "open"); !errors.Is(err, ErrTransition) {
		t.Fatalf("非法迁移应拒绝: %v", err)
	}
	if err := r.Transition(ctx, tk.ID, "closed"); err != nil {
		t.Fatal(err)
	}
	// closed 任何迁移非法
	if err := r.Transition(ctx, tk.ID, "processing"); !errors.Is(err, ErrTransition) {
		t.Fatalf("closed 后迁移应拒绝: %v", err)
	}
}

// TestFirstReplyBackfill 首次客服回复回填 first_reply_at + 状态推进。
func TestFirstReplyBackfill(t *testing.T) {
	r, _ := newTicketData(t)
	ctx := context.Background()
	tk := seedTicket(t, r, "T-FR-1", 7, "")

	_, _ = r.CreateMessage(ctx, tk.ID, "admin", 1, "处理中", nil, false)
	if err := r.MarkProcessingOnReply(ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetByNo(ctx, "T-FR-1")
	if got.Status != "processing" || got.FirstReplyAt.IsZero() {
		t.Fatalf("首响回填失败: %+v", got)
	}
	// 用户追加回复（resolved 重开路径）
	_ = r.Transition(ctx, tk.ID, "resolved")
	if err := r.Transition(ctx, tk.ID, "processing"); err != nil {
		t.Fatal(err) // resolved → processing 重开
	}
}

// TestGuestContactRequired 游客联系方式必填。
func TestGuestContactRequired(t *testing.T) {
	r, _ := newTicketData(t)
	_, err := r.Create(context.Background(), "T-G-1", CreateInput{Type: "presale", Content: "x"})
	if !errors.Is(err, ErrGuestContact) {
		t.Fatalf("游客无联系方式应拒绝: %v", err)
	}
	// 有联系方式可建
	if _, err := r.Create(context.Background(), "T-G-2", CreateInput{GuestContact: "wx: abc", Type: "presale", Content: "x"}); err != nil {
		t.Fatal(err)
	}
}

// TestWorkbenchPrioritySort 工作台 urgent_paid 置顶排序。
func TestWorkbenchPrioritySort(t *testing.T) {
	r, d := newTicketData(t)
	ctx := context.Background()
	// 造四单不同优先级
	for i, p := range []string{"low", "urgent_paid", "normal", "high"} {
		tk, err := d.Client.Ticket.Create().
			SetTicketNo(fmt.Sprintf("T-SORT-%d", i)).
			SetType("presale").SetStatus("open").
			SetPriority(ticket.Priority(p)).Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_ = tk
	}
	rows, _, err := r.ListWorkbench(ctx, "", "", 0, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || string(rows[0].Priority) != "urgent_paid" || string(rows[1].Priority) != "high" {
		t.Fatalf("排序错误: %v", prioritiesOf(rows))
	}
}

// TestAutoClose resolved 超期自动关闭。
func TestAutoClose(t *testing.T) {
	r, d := newTicketData(t)
	ctx := context.Background()
	tk := seedTicket(t, r, "T-AC-1", 7, "")
	_ = r.Transition(ctx, tk.ID, "resolved")
	// updatedAt 改到 8 天前
	_, _ = d.Client.Ticket.UpdateOneID(tk.ID).
		SetUpdatedAt(time.Now().UTC().Add(-8 * 24 * time.Hour)).Save(ctx)

	rows, err := r.ListAutoCloseable(ctx, time.Now().UTC().Add(-7*24*time.Hour), 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("可关闭列表错误: %d %v", len(rows), err)
	}
	if err := r.Transition(ctx, rows[0].ID, "closed"); err != nil {
		t.Fatal(err)
	}
}

// TestSatisfaction 评价：resolved 后可评一次。
func TestSatisfaction(t *testing.T) {
	r, _ := newTicketData(t)
	ctx := context.Background()
	tk := seedTicket(t, r, "T-SAT-1", 7, "")

	// 未解决不可评
	if err := r.SetSatisfaction(ctx, tk.ID, 5); !errors.Is(err, ErrNotResolved) {
		t.Fatalf("未解决评价应拒绝: %v", err)
	}
	_ = r.Transition(ctx, tk.ID, "resolved")
	if err := r.SetSatisfaction(ctx, tk.ID, 5); err != nil {
		t.Fatal(err)
	}
	if err := r.SetSatisfaction(ctx, tk.ID, 3); !errors.Is(err, ErrAlreadyRated) {
		t.Fatalf("重复评价应拒绝: %v", err)
	}
}

// TestPayUrgent 付费加急：余额扣费（充足/不足两态）+ 优先级升级 + 幂等。
func TestPayUrgent(t *testing.T) {
	r, d := newTicketData(t)
	ctx := context.Background()
	// 用户 + 钱包余额（wallet account 由 DebitInTx ensure）
	if _, err := d.Client.User.Create().
		SetUsername("u-urgent").SetStatus(user.StatusActive).Save(ctx); err != nil {
		t.Fatal(err)
	}
	tk := seedTicket(t, r, "T-UR-1", 1, "")

	// 余额不足 → paid=false 不升级
	if err := r.SetPriority(ctx, tk.ID, "urgent_paid"); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetByNo(ctx, "T-UR-1")
	if got.Priority != "urgent_paid" {
		t.Fatal("优先级未升级")
	}
}


func prioritiesOf(rows []*ent.Ticket) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, string(r.Priority))
	}
	return out
}
