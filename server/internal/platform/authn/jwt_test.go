package authn

import (
	"testing"
	"time"
)

func TestAdminSessionAbsoluteDeadline(t *testing.T) {
	signer, err := NewSigner(make([]byte, 32), make([]byte, 32), 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	token, expires, err := signer.IssueUntil(RealmAdmin, 1, "admin", 1, 7, deadline)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.Verify(RealmAdmin, token)
	if err != nil {
		t.Fatal(err)
	}
	if expires.After(deadline) || claims.AuthVersion != 7 || claims.ExpiresAt.Time.After(deadline) {
		t.Fatal("token exceeds session deadline")
	}
	if _, _, err = signer.IssueUntil(RealmAdmin, 1, "admin", 1, 7, time.Now().Add(-time.Second)); err == nil {
		t.Fatal("expired session issued token")
	}
}
