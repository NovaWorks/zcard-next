package admincmd

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/orderaccess"
	"testing"
)

func TestReencryptOrderAccessRetainsCredentialAndScope(t *testing.T) {
	ctx := context.Background()
	client := newReencryptClient(t)
	oldKey := []byte("01234567890123456789012345678901")
	newKey := []byte("abcdefghijklmnopqrstuvwxyz123456")
	oldCipher, _ := inventory.NewCardCipher(oldKey)
	newCipher, _ := inventory.NewCardCipher(newKey)
	token, err := orderaccess.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	secret, err := oldCipher.SealOrderAccess(token, "ROTATE-ORDER", 3)
	if err != nil {
		t.Fatal(err)
	}
	o := client.Order.Create().SetOrderNo("ROTATE-ORDER").SetSubsiteID(3).SetOrderAccessTokenHash(orderaccess.TokenHash(3, "ROTATE-ORDER", token)).SetOrderAccessTokenSecret(secret).SaveX(ctx)
	client.Order.Create().SetOrderNo("RECOVERED-ORDER").SetOrderAccessTokenHash(orderaccess.TokenHash(0, "RECOVERED-ORDER", token)).SaveX(ctx)
	r, s, f, err := ReencryptCards(ctx, client, oldKey, newKey, 1)
	if err != nil || r != 1 || s != 0 || f != 0 {
		t.Fatalf("rotation %d/%d/%d %v", r, s, f, err)
	}
	current := client.Order.GetX(ctx, o.ID)
	raw, err := newCipher.OpenOrderAccess(current.OrderAccessTokenSecret, o.OrderNo, o.SubsiteID)
	if err != nil || raw != token || current.OrderAccessTokenHash != o.OrderAccessTokenHash {
		t.Fatal("rotation lost credential or hash", err)
	}
	if _, err := newCipher.OpenOrderAccess(current.OrderAccessTokenSecret, o.OrderNo, 4); err == nil {
		t.Fatal("ciphertext accepted outside tenant")
	}
	r, s, f, err = ReencryptCards(ctx, client, oldKey, newKey, 1)
	if err != nil || r != 0 || s != 1 || f != 0 {
		t.Fatalf("rotation retry %d/%d/%d %v", r, s, f, err)
	}
}
