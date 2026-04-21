package protocol

import (
	"strings"
	"sync"
	"testing"
)

func TestGenSessionIDShape(t *testing.T) {
	id := GenSessionID("qs")
	if !strings.HasPrefix(id, "qs_") {
		t.Fatalf("prefix: %q", id)
	}
	// "qs_" + 12 hex chars = 15
	if len(id) != 15 {
		t.Fatalf("length: %q", id)
	}
}

func TestGenSessionIDUniqueness(t *testing.T) {
	const n = 10000
	seen := make(map[string]struct{}, n)
	for i := range n {
		id := GenSessionID("cs")
		if _, ok := seen[id]; ok {
			t.Fatalf("collision at %d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestGenSessionIDConcurrent(t *testing.T) {
	// Run under -race to catch any shared state.
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			for range 100 {
				_ = GenSessionID("rs")
			}
		})
	}
	wg.Wait()
}
