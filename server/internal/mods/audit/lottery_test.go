package audit

import (
	"encoding/json"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLotteryAuditAndRequestLogsHidePrizeSecrets(t *testing.T) {
	raw := `{"name":"活动","prizes":[{"name":"奖品","probability":5000,"content":"private-receipt"}]}`
	req := httptest.NewRequest("POST", "/api/v1/admin/lottery/activities", strings.NewReader(raw))
	snapshot := ReadBodyJSON(req)
	b, _ := json.Marshal(snapshot)
	if strings.Contains(string(b), "private-receipt") || !strings.Contains(string(b), "5000") {
		t.Fatal("audit did not redact prize", string(b))
	}
	restored, _ := io.ReadAll(req.Body)
	if string(restored) != raw {
		t.Fatal("audit changed actual request")
	}
	a := &adminv1.LotteryActivity{Id: 1, Prizes: []*adminv1.LotteryPrize{{Content: "private-receipt"}}}
	if strings.Contains(a.Redact(), "private-receipt") {
		t.Fatal("request log leaked prize")
	}
}
