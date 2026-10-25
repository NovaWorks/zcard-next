package captcha

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryStoreExpiryAndCleanup(t *testing.T) {
	s := newMemoryStore()
	s.Set("expired", "1234")
	s.Set("valid", "AbCd")
	entry := s.answers["expired"]
	entry.expiresAt = time.Now().Add(-time.Second)
	s.answers["expired"] = entry
	if s.Verify("expired", "1234", true) {
		t.Fatal("expired answer accepted")
	}
	if !s.Verify("valid", "abcd", false) {
		t.Fatal("valid answer was lost")
	}
	s.answers["cleanup"] = entry
	s.nextCleanup = time.Time{}
	s.Set("new", "5678")
	if _, ok := s.answers["cleanup"]; ok {
		t.Fatal("expired entry was not cleaned up")
	}
	if s.Get("new", false) != "5678" || s.Get("valid", false) != "AbCd" {
		t.Fatal("cleanup removed valid answers")
	}
}

func TestMemoryStoreConcurrentConsume(t *testing.T) {
	s := newMemoryStore()
	s.Set("once", "Ab12")
	var passed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Verify("once", "aB12", true) {
				passed.Add(1)
			}
		}()
	}
	wg.Wait()
	if passed.Load() != 1 {
		t.Fatalf("answer accepted %d times, want once", passed.Load())
	}
}
