package memberlevel

import (
	"context"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestStoreLevelLadderEnabledAndOrdered(t *testing.T) {
	_, repo, _, _ := newMemberLevelEnv(t)
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 9})
	for _, v := range []struct {
		name, kind     string
		discount, sort int32
		enabled        bool
	}{{"VIP", "both_and", 8500, 2, true}, {"隐藏", "recharge", 8000, 3, false}, {"普通", "both_or", 10000, 1, true}} {
		if _, e := repo.CreateLevel(ctx, v.name, v.kind, 10000, 5000, v.discount, v.sort, v.enabled, nil); e != nil {
			t.Fatal(e)
		}
	}
	reply, err := NewStoreMemberLevelService(repo, nil).GetMyLevel(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Levels) != 2 || reply.Levels[0].Name != "普通" || reply.Levels[1].Discount != 8500 || reply.Levels[1].ThresholdType != "both_and" {
		t.Fatalf("ladder: %v", reply.Levels)
	}
}
