package supply

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	kerrors "github.com/go-kratos/kratos/v3/errors"
	"testing"
)

func TestPublicCSN19SetupUnavailable(t *testing.T) {
	repo, d := newTestRepo(t)
	svc := NewAdminSupplyService(repo, nil)
	_, err := svc.CreateConnection(context.Background(), &adminv1.CreateConnectionRequest{Driver: "csn19", BaseUrl: "https://19csn.com/api", Credentials: `{"api_key":"fixture-only"}`})
	if !kerrors.IsBadRequest(err) || kerrors.Reason(err) != "supply.DRIVER_UNAVAILABLE" || d.Client.SupplyConnection.Query().CountX(context.Background()) != 0 {
		t.Fatal("public instance accepted native supplier configuration", err)
	}
}
