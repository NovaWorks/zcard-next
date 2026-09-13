package businessday

import (
	"testing"
	"time"
)

func TestMidnightIndependentOfHostTimezone(t *testing.T) {
	at := time.Date(2026, 9, 12, 16, 0, 0, 0, time.UTC)
	for _, loc := range []*time.Location{time.UTC, time.FixedZone("Vietnam", 7*3600), time.FixedZone("Pacific", -7*3600)} {
		if Date(at.In(loc), "20060102") != "20260913" || !Start(at.In(loc)).Equal(at) {
			t.Fatal("incorrect Beijing boundary", loc)
		}
	}
	if Date(at.Add(-time.Nanosecond), "20060102") != "20260912" {
		t.Fatal("early rollover")
	}
}
