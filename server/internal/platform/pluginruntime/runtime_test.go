package pluginruntime

import (
	"context"
	"encoding/json"
	"fmt"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"os"
	"sync"
	"testing"
	"time"
)

func input(t *testing.T) pc.Input {
	t.Helper()
	raw, e := os.ReadFile("../plugincontract/testdata/input-valid.json")
	if e != nil {
		t.Fatal(e)
	}
	var in pc.Input
	if e = json.Unmarshal(raw, &in); e != nil {
		t.Fatal(e)
	}
	return in
}
func TestRuntimeBoundaries(t *testing.T) {
	for i, script := range []string{
		`throw new Error('init')`, `while(true){}`, `var evaluate=3`,
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if _, e := NewEngine().Prepare(context.Background(), fmt.Sprint(i), script); e == nil {
				t.Fatal("bad initialization accepted")
			}
		})
	}
	for i, script := range []string{
		`function evaluate(){while(true){}}`,
		`function evaluate(){throw new Error('private data')}`,
		`function evaluate(){return {allow:true,reason:'MEMBER_LEVEL_DENIED'}}`,
		`function evaluate(){return {allow:true,reason:'OK',extra:1}}`,
		`function evaluate(){return {get allow(){while(true){}},reason:'OK'}}`,
		`function evaluate(input){input.member.effectiveLevelId='999';return {allow:true,reason:'OK'}}`,
		`async function evaluate(){return {allow:true,reason:'OK'}}`,
		`function evaluate(){return evaluate()}`,
	} {
		t.Run("execution-"+fmt.Sprint(i), func(t *testing.T) {
			r, e := NewEngine().Prepare(context.Background(), fmt.Sprint(i), script)
			if e != nil {
				t.Fatal(e)
			}
			start := time.Now()
			if _, e = r.Evaluate(context.Background(), input(t)); e == nil {
				t.Fatal("bad decision accepted")
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("execution failed to stop")
			}
		})
	}
}
func TestFreshVMAndConcurrentIsolation(t *testing.T) {
	e := NewEngine()
	// Race instrumentation can exceed 50ms under parallel package builds. Test
	// VM isolation independently; timeout tests retain the production budget.
	e.hookBudget = 500 * time.Millisecond
	r, err := e.Prepare(context.Background(), "fresh", `var count=0; function evaluate(input){count++;return {allow:count===1 && typeof fetch==='undefined' && typeof require==='undefined' && typeof setTimeout==='undefined' && typeof Promise==='undefined' && input.subsiteId===input.productId && input.member.effectiveLevelId===input.config.allowedLevelIds[0],reason:'OK'}}`)
	if err != nil {
		t.Fatal(err)
	}
	in := input(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			in := in
			in.SubsiteID = pc.Decimal(fmt.Sprint(i + 1))
			in.ProductID = in.SubsiteID
			in.Member.EffectiveLevelID = in.SubsiteID
			in.Config.AllowedLevelIDs = []pc.Decimal{in.SubsiteID}
			defer wg.Done()
			for j := 0; j < 3; j++ {
				v, err := r.Evaluate(context.Background(), in)
				if err != nil || !v.Allow {
					t.Errorf("VM state leaked: %+v %v", v, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	in.SubsiteID = in.ProductID
	in.Member.EffectiveLevelID = in.Config.AllowedLevelIDs[0]
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = r.Evaluate(ctx, in); err == nil {
		t.Fatal("canceled invocation accepted")
	}
	if v, err := r.Evaluate(context.Background(), in); err != nil || !v.Allow {
		t.Fatal("old cancellation contaminated new invocation", err)
	}
	for i := 0; i < cap(e.admission); i++ {
		e.admission <- struct{}{}
	}
	if _, err = r.Evaluate(context.Background(), in); err == nil {
		t.Fatal("queue limit bypassed")
	}
	for i := 0; i < cap(e.admission); i++ {
		<-e.admission
	}
}
func TestCircuitProbeAndClose(t *testing.T) {
	r, err := NewEngine().Prepare(context.Background(), "circuit", `function evaluate(input){if(input.member.effectiveLevelId!=='2')throw new Error('fault');return {allow:true,reason:'OK'}}`)
	if err != nil {
		t.Fatal(err)
	}
	in := input(t)
	in.Member.EffectiveLevelID = "1"
	for i := 0; i < 3; i++ {
		if _, err = r.Evaluate(context.Background(), in); err == nil {
			t.Fatal("expected fault")
		}
	}
	if !r.Faulted() {
		t.Fatal("circuit not open")
	}
	in.Member.EffectiveLevelID = "2"
	if _, err = r.Evaluate(context.Background(), in); err == nil {
		t.Fatal("cooldown bypassed")
	}
	r.mu.Lock()
	r.until = time.Now().Add(-time.Second)
	r.mu.Unlock()
	if _, err = r.Evaluate(context.Background(), in); err != nil || r.Faulted() {
		t.Fatal("probe did not recover", err)
	}
	r.Close()
	if _, err = r.Evaluate(context.Background(), in); err == nil {
		t.Fatal("closed runtime executed")
	}
}

// Execute every available-runtime P0 business vector against the shipped JS,
// including large decimal IDs and all three supply channel names. Unavailable
// package/resolver/entitlement states belong to the host, not to script logic.
func TestP2SampleContractMatrix(t *testing.T) {
	source, err := os.ReadFile("../../../../examples/plugins/member-purchase-gate/main.js")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../plugincontract/testdata/business-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID           string    `json:"id"`
		Input        pc.Input  `json:"input"`
		Availability string    `json:"availability"`
		Expected     pc.Reason `json:"expected"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	r, err := NewEngine().Prepare(context.Background(), "sample-matrix", string(source))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		if tc.Availability != "ready" {
			continue
		}
		t.Run(tc.ID, func(t *testing.T) {
			out, err := r.Evaluate(context.Background(), tc.Input)
			if err != nil || out.Reason != tc.Expected || out.Allow != (tc.Expected == pc.OK) {
				t.Fatalf("sample contract mismatch: %+v %v", out, err)
			}
		})
	}
}
