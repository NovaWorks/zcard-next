// Package pluginruntime executes first-party scripts with fresh, bounded VMs.
// It is not a memory/process sandbox for hostile third-party code.
package pluginruntime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/dop251/goja"
)

const HookBudget = 50 * time.Millisecond
const RequestBudget = 250 * time.Millisecond
const MaxItems = 100

// Engine bounds all plugins together, including queued callers. Cached programs
// are immutable; no Runtime or JS object is reused across invocations.
type Engine struct {
	hookBudget         time.Duration
	mu                 sync.Mutex
	programs           map[string]*goja.Program
	order              []string
	workers, admission chan struct{}
}

func NewEngine() *Engine {
	return &Engine{hookBudget: HookBudget, programs: map[string]*goja.Program{}, workers: make(chan struct{}, 16), admission: make(chan struct{}, 48)}
}

type Runtime struct {
	digest   string
	engine   *Engine
	program  *goja.Program
	closed   atomic.Bool
	mu       sync.Mutex
	failures int
	until    time.Time
	probe    bool
}

func unavailable(detail string) error { return &pc.Error{Code: pc.Unavailable, Detail: detail} }
func (e *Engine) Prepare(ctx context.Context, digest, source string) (*Runtime, error) {
	if ctx.Err() != nil {
		return nil, unavailable("preparation canceled")
	}
	if len(source) > pc.MaxScriptBytes {
		return nil, unavailable("script size limit")
	}
	e.mu.Lock()
	p := e.programs[digest]
	e.mu.Unlock()
	if p == nil {
		var err error
		p, err = goja.Compile("plugin.js", source, true)
		if err != nil {
			slog.Warn("plugin preparation failed", "digest", digest, "reason", "script compilation failed")
			return nil, unavailable("script compilation failed")
		}
		e.mu.Lock()
		if len(e.order) >= 128 {
			delete(e.programs, e.order[0])
			e.order = e.order[1:]
		}
		if e.programs[digest] == nil {
			e.programs[digest] = p
			e.order = append(e.order, digest)
		}
		e.mu.Unlock()
	}
	r := &Runtime{engine: e, program: p, digest: digest}
	if _, err := r.invoke(ctx, nil); err != nil {
		slog.Warn("plugin preparation failed", "digest", digest, "reason", safeReason(err))
		return nil, err
	}
	return r, nil
}
func (r *Runtime) Close() error  { r.closed.Store(true); return nil }
func (r *Runtime) Faulted() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.failures >= 3 }
func (r *Runtime) Evaluate(ctx context.Context, in pc.Input) (pc.Decision, error) {
	var out pc.Decision
	raw, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	if err = pc.Validate(pc.InputKind, raw); err != nil {
		return out, unavailable("invalid host input")
	}
	r.mu.Lock()
	if r.failures >= 3 {
		if time.Now().Before(r.until) || r.probe {
			r.mu.Unlock()
			return out, unavailable("runtime circuit open")
		}
		r.probe = true
	}
	r.mu.Unlock()
	result, err := r.invoke(ctx, raw)
	if err == nil {
		if pc.Validate(pc.DecisionKind, result) != nil {
			err = unavailable("invalid decision")
		}
		if err == nil {
			if json.Unmarshal(result, &out) != nil {
				err = unavailable("invalid decision")
			}
		}
	}
	r.mu.Lock()
	r.probe = false
	if err != nil {
		r.failures++
		if r.failures == 1 || r.failures == 3 {
			slog.Warn("plugin hook unavailable", "digest", r.digest, "reason", safeReason(err), "consecutive_failures", r.failures)
		}
		if r.failures >= 3 {
			r.until = time.Now().Add(5 * time.Second)
		}
	} else {
		r.failures = 0
		r.until = time.Time{}
	}
	r.mu.Unlock()
	if err != nil {
		return pc.Decision{}, unavailable("runtime decision unavailable")
	}
	return out, nil
}

// Capture validators before untrusted code can replace builtins. Only own data
// properties with primitive values may cross the boundary; getters, promises,
// toJSON hooks and arbitrary object graphs never escape to Go.
const bootstrap = `(function(){
 const keys=Object.keys, names=Object.getOwnPropertyNames, desc=Object.getOwnPropertyDescriptor, freeze=Object.freeze, parse=JSON.parse, stringify=JSON.stringify;
 function deep(v){if(v && typeof v==='object'){const ks=keys(v);for(let i=0;i<ks.length;i++){deep(v[ks[i]]);}freeze(v);}return v;}
 globalThis.Promise=undefined;
 return function(fn,raw){
  if(typeof fn!=='function')throw new Error('missing evaluate');
  if(raw===null)return '';
  const out=fn(deep(parse(raw)));
  if(!out || typeof out!=='object' || names(out).length!==2)throw new Error('invalid decision');
  const a=desc(out,'allow'),r=desc(out,'reason');
  if(!a||!r||!('value' in a)||!('value' in r)||typeof a.value!=='boolean'||typeof r.value!=='string'||r.value.length>64)throw new Error('invalid decision');
  return stringify({allow:a.value,reason:r.value});
 };
})()`

func (r *Runtime) invoke(parent context.Context, raw []byte) (result []byte, err error) {
	if r.closed.Load() {
		return nil, unavailable("runtime closed")
	}
	ctx, cancel := context.WithTimeout(parent, r.engine.hookBudget)
	defer cancel()
	select {
	case r.engine.admission <- struct{}{}:
		defer func() { <-r.engine.admission }()
	default:
		return nil, unavailable("runtime queue full")
	}
	select {
	case r.engine.workers <- struct{}{}:
		defer func() { <-r.engine.workers }()
	case <-ctx.Done():
		return nil, unavailable("runtime admission timeout")
	}
	vm := goja.New()
	vm.SetMaxCallStackSize(128)
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-ctx.Done():
			vm.Interrupt(errors.New("execution interrupted"))
		case <-done:
		}
	}()
	defer func() {
		close(done)
		<-exited
		if recover() != nil {
			result = nil
			err = unavailable("runtime panic")
		}
		if ctx.Err() != nil {
			result = nil
			err = unavailable("runtime timeout")
		}
	}()
	validation, e := vm.RunString(bootstrap)
	if e != nil {
		return nil, unavailable("runtime initialization failed")
	}
	validate, ok := goja.AssertFunction(validation)
	if !ok {
		return nil, unavailable("runtime validator missing")
	}
	if _, e = vm.RunProgram(r.program); e != nil {
		return nil, unavailable("script initialization failed")
	}
	value := goja.Null()
	if raw != nil {
		value = vm.ToValue(string(raw))
	}
	v, e := validate(goja.Undefined(), vm.Get("evaluate"), value)
	if e != nil {
		return nil, unavailable("script execution failed")
	}
	result = []byte(v.String())
	if len(result) > pc.MaxJSONBytes {
		return nil, unavailable("runtime output limit")
	}
	return result, nil
}

// Only errors produced by this adapter are eligible for diagnostics. Script
// exception text, input JSON and member details never enter logs.
func safeReason(err error) string {
	var e *pc.Error
	if errors.As(err, &e) {
		return e.Detail
	}
	return "runtime failure"
}
