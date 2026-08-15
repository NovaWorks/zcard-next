package id

import (
	"sync"
	"testing"
)

func TestGeneratorUnique(t *testing.T) {
	g, err := NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	const n = 10000
	seen := make(map[int64]struct{}, n)
	for i := 0; i < n; i++ {
		v, err := g.Next()
		if err != nil {
			t.Fatal(err)
		}
		if _, dup := seen[v]; dup {
			t.Fatalf("重复 ID: %d", v)
		}
		seen[v] = struct{}{}
	}
}

func TestGeneratorConcurrent(t *testing.T) {
	g, _ := NewGenerator(7)
	var mu sync.Mutex
	seen := make(map[int64]struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]int64, 0, 1000)
			for i := 0; i < 1000; i++ {
				v, err := g.Next()
				if err != nil {
					t.Error(err)
					return
				}
				local = append(local, v)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, v := range local {
				if _, dup := seen[v]; dup {
					t.Errorf("并发重复 ID: %d", v)
					return
				}
				seen[v] = struct{}{}
			}
		}()
	}
	wg.Wait()
}

func TestWorkerIDRange(t *testing.T) {
	if _, err := NewGenerator(-1); err == nil {
		t.Error("负 workerID 应报错")
	}
	if _, err := NewGenerator(1024); err == nil {
		t.Error("workerID 超界应报错")
	}
}
