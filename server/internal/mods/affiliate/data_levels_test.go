package affiliate

import (
	"context"
	"fmt"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"google.golang.org/protobuf/types/known/emptypb"
	"testing"
)

func TestConfiguredLevelsAndZeroRates(t *testing.T) {
	for _, tc := range []struct {
		config  string
		amounts map[int8]int64
	}{
		{"levels=1", map[int8]int64{1: 500}},
		{"levels=2", map[int8]int64{1: 500, 2: 200}},
		{"levels=3", map[int8]int64{1: 500, 2: 200, 3: 100}},
		{"levels=1|rate_l1=20|rate_l2=0|rate_l3=0|self_buy=true", map[int8]int64{1: 20}},
		{"levels=3|rate_l1=0|rate_l2=0|rate_l3=0", map[int8]int64{}},
		{"levels=3|rate_l2=0", map[int8]int64{1: 500, 3: 100}},
		{"levels=1|enabled=false", map[int8]int64{}},
	} {
		t.Run(tc.config, func(t *testing.T) {
			svc, repo, _ := newEngine(t, newFakeWallet(), tc.config)
			ctx := context.Background()
			for i := 0; i < 2; i++ {
				if err := svc.OnOrderPaid(ctx, paidEnv(123, 10, 7, 8, 9, 10000)); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := repo.ListByOrder(ctx, 123)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != len(tc.amounts) {
				t.Fatalf("got %d commission rows, want %d", len(rows), len(tc.amounts))
			}
			for _, row := range rows {
				if row.Amount != tc.amounts[row.Tier] {
					t.Fatalf("L%d: got %d, want %d", row.Tier, row.Amount, tc.amounts[row.Tier])
				}
			}
		})
	}
}

func TestTeamQueriesRespectConfiguredLevels(t *testing.T) {
	repo, d := newAffiliateData(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		q := d.Client.User.Create().SetUsername(fmt.Sprintf("member%d", i))
		switch i {
		case 1:
			q.SetInviteL1(99)
		case 2:
			q.SetInviteL2(99)
		case 3:
			q.SetInviteL3(99)
		}
		if _, err := q.Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for levels := 1; levels <= 3; levels++ {
		rows, total, err := repo.ListTeam(ctx, 99, 0, levels, 1, 1)
		if err != nil || total != levels || len(rows) != 1 {
			t.Fatalf("levels %d: rows %d total %d err %v", levels, len(rows), total, err)
		}
		for tier := 1; tier <= 3; tier++ {
			rows, total, err = repo.ListTeam(ctx, 99, tier, levels, 1, 15)
			want := 0
			if tier <= levels {
				want = 1
			}
			if err != nil || total != want || len(rows) != want {
				t.Fatalf("levels %d tier %d: rows %d total %d err %v", levels, tier, len(rows), total, err)
			}
		}
	}
}

func TestStoreAffiliateUsesCurrentLevels(t *testing.T) {
	repo, d := newAffiliateData(t)
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 99})
	if _, err := d.Client.User.Create().SetID(99).SetUsername("referrer").Save(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		q := d.Client.User.Create().SetUsername(fmt.Sprintf("team%d", i))
		switch i {
		case 1:
			q.SetInviteL1(99)
		case 2:
			q.SetInviteL2(99)
		case 3:
			q.SetInviteL3(99)
		}
		if _, err := q.Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	settings := fakeSettings{kv: map[string]string{"levels": "1"}}
	svc := NewStoreAffiliateService(repo, identity.NewUserRepo(d), settings)
	for levels := 1; levels <= 3; levels++ {
		settings.kv["levels"] = fmt.Sprint(levels)
		my, err := svc.MyAffiliate(ctx, &emptypb.Empty{})
		if err != nil {
			t.Fatal(err)
		}
		if my.TeamL1 != 1 || (my.TeamL2 == 1) != (levels >= 2) || (my.TeamL3 == 1) != (levels >= 3) {
			t.Fatalf("levels %d counts: %v", levels, my)
		}
		team, err := svc.ListTeam(ctx, &storefrontv1.ListTeamRequest{})
		if err != nil || team.GetTotal() != int64(levels) {
			t.Fatalf("levels %d team: %v %v", levels, team, err)
		}
	}
}
