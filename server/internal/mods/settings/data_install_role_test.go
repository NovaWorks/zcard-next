package settings

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
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/adminrole"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/adminuser"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rolepermission"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
)

func TestInstallChoosesSuperAdminAfterExistingRole(t *testing.T) {
	ctx := context.Background()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:install-role-%d?mode=memory&cache=shared", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err = client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	limited := client.AdminRole.Create().SetCode("market_publisher").SetName("Existing limited role").SaveX(ctx)
	client.RolePermission.Create().SetRoleID(limited.ID).SetPermissionCode("market:read").SaveX(ctx)
	input := InstallInput{AdminUsername: "fresh-admin", AdminPassword: "isolated-install-password", SiteName: "Isolated install"}
	if err = Install(ctx, d, input); err != nil {
		t.Fatal(err)
	}
	admin := client.AdminUser.Query().Where(adminuser.Username(input.AdminUsername)).OnlyX(ctx)
	super := client.AdminRole.Query().Where(adminrole.Code("super_admin")).OnlyX(ctx)
	if admin.RoleID != super.ID || admin.RoleID == limited.ID {
		t.Fatalf("installer chose role %d instead of super_admin %d", admin.RoleID, super.ID)
	}
	if !client.RolePermission.Query().Where(rolepermission.RoleID(super.ID), rolepermission.PermissionCode("*")).ExistX(ctx) {
		t.Fatal("installed administrator has no super-admin wildcard")
	}
	if client.RolePermission.Query().Where(rolepermission.RoleID(limited.ID), rolepermission.PermissionCode("*")).ExistX(ctx) {
		t.Fatal("existing limited role was elevated")
	}
	if err = Install(ctx, d, input); !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("repeat install changed behavior: %v", err)
	}
	if client.AdminUser.Query().CountX(ctx) != 1 {
		t.Fatal("repeat install created another administrator")
	}
}
