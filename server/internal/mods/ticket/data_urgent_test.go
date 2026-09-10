package ticket

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/ticket"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	walletmod "github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
)

type urgentSettings struct {
	raw string
	err error
}

func (s *urgentSettings) Get(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(s.raw), s.err
}
func (s *urgentSettings) GetDefault(ctx context.Context, g, k string, def json.RawMessage) (json.RawMessage, error) {
	if s.raw == "" && s.err == nil {
		return def, nil
	}
	return s.Get(ctx, g, k)
}
func feePtr(v int64) *int64 { return &v }

func TestUrgentQuotedFeeAndAtomicDebit(t *testing.T) {
	r, d := newTicketData(t)
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7, Realm: authn.RealmUser})
	cfg := &urgentSettings{raw: "350"}
	s := NewStoreTicketService(r, nil, walletmod.ProvidePortWallet(walletmod.NewWalletRepoImpl(d)), cfg, nil, nil)
	tk := seedTicket(t, r, "T-PAID", 7, "")
	acc := d.Client.WalletAccount.Create().SetUserID(7).SetAvailable(1000).SaveX(ctx)
	detail, err := s.GetTicket(ctx, &storefrontv1.GetTicketRequest{TicketNo: tk.TicketNo})
	if err != nil || !detail.UrgentAvailable || detail.UrgentFeeCents != 350 {
		t.Fatalf("quote %+v %v", detail, err)
	}
	for _, fee := range []*int64{nil, feePtr(0), feePtr(349)} {
		if _, err := s.PayUrgent(ctx, &storefrontv1.PayUrgentRequest{TicketNo: tk.TicketNo, ExpectedFeeCents: fee}); err == nil {
			t.Fatal("unconfirmed or mismatched quote charged")
		}
	}
	for i := 0; i < 2; i++ {
		res, err := s.PayUrgent(ctx, &storefrontv1.PayUrgentRequest{TicketNo: tk.TicketNo, ExpectedFeeCents: feePtr(350)})
		if err != nil || !res.Paid || res.FeeCents != 350 || res.AlreadyUrgent != (i == 1) {
			t.Fatalf("pay %+v %v", res, err)
		}
	}
	if d.Client.WalletAccount.GetX(ctx, acc.ID).Available != 650 || d.Client.WalletTransaction.Query().CountX(ctx) != 1 {
		t.Fatal("debit was incorrect or repeated")
	}
	if got := d.Client.Ticket.GetX(ctx, tk.ID); got.Priority != ticket.PriorityUrgentPaid || got.SLADueAt.IsZero() {
		t.Fatal("priority/SLA not committed")
	}
	// Failure after wallet balance UPDATE must roll back both wallet and ticket.
	fail := seedTicket(t, r, "T-ROLLBACK", 7, "")
	d.Client.WalletTransaction.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			return nil, errors.New("injected ledger write failure")
		})
	})
	if _, err := s.PayUrgent(ctx, &storefrontv1.PayUrgentRequest{TicketNo: fail.TicketNo, ExpectedFeeCents: feePtr(350)}); err == nil {
		t.Fatal("ledger failure hidden")
	}
	if d.Client.WalletAccount.GetX(ctx, acc.ID).Available != 650 || d.Client.Ticket.GetX(ctx, fail.ID).Priority == ticket.PriorityUrgentPaid {
		t.Fatal("partial debit/priority committed")
	}
}

func TestUrgentFreeConfigFailureAndOwnership(t *testing.T) {
	r, d := newTicketData(t)
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7, Realm: authn.RealmUser})
	cfg := &urgentSettings{raw: "0"}
	s := NewStoreTicketService(r, nil, nil, cfg, nil, nil)
	for i, raw := range []string{"0", `{"urgent_fee":0}`} {
		cfg.raw = raw
		tk := seedTicket(t, r, "T-FREE-"+string(rune('A'+i)), 7, "")
		res, err := s.PayUrgent(ctx, &storefrontv1.PayUrgentRequest{TicketNo: tk.TicketNo, ExpectedFeeCents: feePtr(0)})
		if err != nil || !res.Paid || res.FeeCents != 0 {
			t.Fatalf("free %+v %v", res, err)
		}
	}
	tk := seedTicket(t, r, "T-INVALID", 7, "")
	for _, raw := range []string{"null", "{}", "-1", `"350"`, "broken", "1.5"} {
		cfg.raw = raw
		res, err := s.GetTicket(ctx, &storefrontv1.GetTicketRequest{TicketNo: tk.TicketNo})
		if err != nil || res.UrgentAvailable || res.UrgentError == "" {
			t.Fatalf("invalid quote %q %+v %v", raw, res, err)
		}
		if _, err := s.PayUrgent(ctx, &storefrontv1.PayUrgentRequest{TicketNo: tk.TicketNo, ExpectedFeeCents: feePtr(0)}); err == nil {
			t.Fatal("invalid config became free")
		}
	}
	cfg.raw = "0"
	cfg.err = errors.New("settings offline")
	if _, err := s.PayUrgent(ctx, &storefrontv1.PayUrgentRequest{TicketNo: tk.TicketNo, ExpectedFeeCents: feePtr(0)}); err == nil {
		t.Fatal("settings read error became free")
	}
	cfg.err = nil
	for _, other := range []context.Context{context.Background(), identity.WithClaims(context.Background(), &authn.Claims{Subject: 8, Realm: authn.RealmUser})} {
		if _, err := s.PayUrgent(other, &storefrontv1.PayUrgentRequest{TicketNo: tk.TicketNo, ExpectedFeeCents: feePtr(0)}); err == nil {
			t.Fatal("unauthorized urgent")
		}
	}
	d.Client.Ticket.UpdateOneID(tk.ID).SetStatus(ticket.StatusClosed).SaveX(ctx)
	if _, err := s.PayUrgent(ctx, &storefrontv1.PayUrgentRequest{TicketNo: tk.TicketNo, ExpectedFeeCents: feePtr(0)}); err == nil {
		t.Fatal("closed ticket expedited")
	}
	if d.Client.WalletTransaction.Query().CountX(ctx) != 0 {
		t.Fatal("free/failed operation created ledger")
	}
}

func TestUrgentConcurrentRequestsAndInsufficientBalance(t *testing.T) {
	r, d := newTicketData(t)
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7, Realm: authn.RealmUser})
	s := NewStoreTicketService(r, nil, walletmod.ProvidePortWallet(walletmod.NewWalletRepoImpl(d)), &urgentSettings{raw: "350"}, nil, nil)
	tk := seedTicket(t, r, "T-CONCURRENT", 7, "")
	acc := d.Client.WalletAccount.Create().SetUserID(7).SetAvailable(500).SaveX(ctx)
	start := make(chan struct{})
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			<-start
			_, err := s.PayUrgent(ctx, &storefrontv1.PayUrgentRequest{TicketNo: tk.TicketNo, ExpectedFeeCents: feePtr(350)})
			done <- err
		}()
	}
	close(start)
	for i := 0; i < 8; i++ {
		<-done
	} // SQLite may reject concurrent writers; retry must remain idempotent.
	if _, err := s.PayUrgent(ctx, &storefrontv1.PayUrgentRequest{TicketNo: tk.TicketNo, ExpectedFeeCents: feePtr(350)}); err != nil {
		t.Fatal(err)
	}
	if d.Client.WalletTransaction.Query().CountX(ctx) != 1 || d.Client.WalletAccount.GetX(ctx, acc.ID).Available != 150 {
		t.Fatal("concurrent debit was repeated")
	}
	other := seedTicket(t, r, "T-INSUFFICIENT", 7, "")
	if _, err := s.PayUrgent(ctx, &storefrontv1.PayUrgentRequest{TicketNo: other.TicketNo, ExpectedFeeCents: feePtr(350)}); err == nil {
		t.Fatal("insufficient balance accepted")
	}
	if d.Client.Ticket.GetX(ctx, other.ID).Priority == ticket.PriorityUrgentPaid || d.Client.WalletAccount.GetX(ctx, acc.ID).Available != 150 || d.Client.WalletTransaction.Query().CountX(ctx) != 1 {
		t.Fatal("insufficient balance changed ticket or wallet")
	}
}
