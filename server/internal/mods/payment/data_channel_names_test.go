package payment

import (
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"testing"
)

func TestPaymentChannelNamesPreserveIdentity(t *testing.T) {
	for _, tc := range []struct{ driver, name, want string }{
		{"epusdt", "EPUSDT / GM Pay（多链多币种）", "GM Pay"},
		{"epusdt", "EPUSDT / GM Pay（多链多币种） 10", "GM Pay 10"},
		{"epusdt", "我的收款账户", "我的收款账户"},
		{"bepusdt", "BEpusdt", "BEpusdt"},
		{"bepusdt", "EPUSDT / GM Pay（多链多币种）", "EPUSDT / GM Pay（多链多币种）"},
	} {
		ch := &ent.PaymentChannel{ID: 7, Driver: tc.driver, Name: tc.name, Code: "original-code", Config: []byte("sealed")}
		out := ToChannelPB(ch)
		if out.Name != tc.want || out.Driver != tc.driver || out.Code != ch.Code || out.Id != ch.ID || ch.Name != tc.name || string(ch.Config) != "sealed" {
			t.Fatalf("name normalization changed identity: %v", out)
		}
	}
}
