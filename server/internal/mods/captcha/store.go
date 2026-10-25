package captcha

import (
	"strings"
	"sync"
	"time"
)

// memoryStore 将写入、消费和过期清理放在同一把锁内。
// base64Captcha v1.3.8 的内置 store 在锁外访问清理计数，不能用于并发生成。
type memoryStore struct {
	mu          sync.Mutex
	answers     map[string]storedAnswer
	nextCleanup time.Time
}

type storedAnswer struct {
	value     string
	expiresAt time.Time
}

func newMemoryStore() *memoryStore {
	return &memoryStore{answers: make(map[string]storedAnswer)}
}

func (s *memoryStore) Set(id, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if !now.Before(s.nextCleanup) {
		for id, answer := range s.answers {
			if !now.Before(answer.expiresAt) {
				delete(s.answers, id)
			}
		}
		s.nextCleanup = now.Add(time.Minute)
	}
	s.answers[id] = storedAnswer{value: value, expiresAt: now.Add(300 * time.Second)}
	return nil
}

func (s *memoryStore) Get(id string, clear bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	answer, ok := s.answers[id]
	if !ok {
		return ""
	}
	expired := !time.Now().Before(answer.expiresAt)
	if clear || expired {
		delete(s.answers, id)
	}
	if expired {
		return ""
	}
	return answer.value
}

func (s *memoryStore) Verify(id, answer string, clear bool) bool {
	if id == "" || answer == "" {
		return false
	}
	return strings.EqualFold(s.Get(id, clear), answer)
}
