package ticket

import (
	"context"
	"errors"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	walletmod "github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
)

func TestTicketEventFailureRollsBackBusinessChange(t *testing.T) {
	for _, kind := range []string{"create", "user-reply", "staff-reply", "urgent"} {
		t.Run(kind, func(t *testing.T) {
			repo, d := newTicketData(t)
			ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7, Realm: authn.RealmUser})
			ticket := seedTicket(t, repo, "T-ROLLBACK-EVENT", 7, "")
			d.Client.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, errors.New("event write failure") })
			})
			gen, err := id.NewGenerator(1)
			if err != nil {
				t.Fatal(err)
			}
			account := d.Client.WalletAccount.Create().SetUserID(7).SetAvailable(1000).SaveX(ctx)
			store := NewStoreTicketService(repo, gen, walletmod.ProvidePortWallet(walletmod.NewWalletRepoImpl(d)), &urgentSettings{raw: "350"}, data.NewOutboxWriter(d), nil)
			admin := NewAdminTicketService(repo, data.NewOutboxWriter(d))
			switch kind {
			case "create":
				_, err = store.CreateTicket(ctx, &storefrontv1.CreateTicketRequest{Type: "presale", Content: "new"})
			case "user-reply":
				_, err = store.ReplyTicket(ctx, &storefrontv1.ReplyTicketRequest{TicketNo: ticket.TicketNo, Content: "followup"})
			case "staff-reply":
				_, err = admin.ReplyTicket(ctx, &adminv1.AdminReplyRequest{TicketNo: ticket.TicketNo, Content: "reply"})
			case "urgent":
				fee := int64(350)
				_, err = store.PayUrgent(ctx, &storefrontv1.PayUrgentRequest{TicketNo: ticket.TicketNo, ExpectedFeeCents: &fee})
			}
			if d.Client.WalletAccount.GetX(ctx, account.ID).Available != 1000 || d.Client.WalletTransaction.Query().CountX(ctx) != 0 {
				t.Fatal("failed notification retained a charge")
			}
			if err == nil {
				t.Fatal("event failure ignored")
			}
			if d.Client.Ticket.Query().CountX(ctx) != 1 || d.Client.TicketMessage.Query().CountX(ctx) != 1 || d.Client.OutboxEvent.Query().CountX(ctx) != 0 {
				t.Fatal("partially committed ticket operation")
			}
			got := d.Client.Ticket.GetX(ctx, ticket.ID)
			if got.Priority != "normal" || got.Status != "open" || !got.FirstReplyAt.IsZero() {
				t.Fatal("partial state committed", got)
			}
		})
	}
}
