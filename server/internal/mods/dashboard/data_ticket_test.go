package dashboard

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/ticket"
	"testing"
)

func TestTicketPendingTracksWorkbenchStates(t *testing.T) {
	d := newDashboardData(t)
	repo := NewDashboardRepoImpl(d)
	ctx := context.Background()
	for i, status := range []ticket.Status{ticket.StatusOpen, ticket.StatusProcessing, ticket.StatusResolved, ticket.StatusClosed} {
		for j, priority := range []ticket.Priority{ticket.PriorityNormal, ticket.PriorityUrgentPaid} {
			d.Client.Ticket.Create().SetTicketNo(fmt.Sprintf("T-%d-%d", i, j)).SetType(ticket.TypePresale).SetStatus(status).SetPriority(priority).SaveX(ctx)
		}
	}
	check := func(wantOpen, wantProcessing, wantUrgent int64) {
		t.Helper()
		open, processing, urgent, err := repo.GetTicketPending(ctx)
		if err != nil || open != wantOpen || processing != wantProcessing || urgent != wantUrgent {
			t.Fatalf("got %d/%d/%d err %v", open, processing, urgent, err)
		}
	}
	check(2, 2, 2)
	d.Client.Ticket.Update().Where(ticket.TicketNo("T-0-1")).SetStatus(ticket.StatusProcessing).ExecX(ctx)
	check(1, 3, 2)
	d.Client.Ticket.Update().SetStatus(ticket.StatusClosed).ExecX(ctx)
	check(0, 0, 0)
	d.Client.Close()
	if _, _, _, err := repo.GetTicketPending(ctx); err == nil {
		t.Fatal("query failure silently reported as zero")
	}
}
