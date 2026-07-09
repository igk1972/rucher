// SPDX-License-Identifier: AGPL-3.0-or-later

package parallel

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestMapPreservesOrder(t *testing.T) {
	in := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	got := Map(in, 3, func(n int) int { return n * n })
	if len(got) != len(in) {
		t.Fatalf("len = %d, want %d", len(got), len(in))
	}
	for i, n := range in {
		if got[i] != n*n {
			t.Fatalf("got[%d] = %d, want %d", i, got[i], n*n)
		}
	}
}

// TestMapBoundsConcurrency asserts fn never runs more than `limit` times at once.
func TestMapBoundsConcurrency(t *testing.T) {
	const limit = 3
	items := make([]int, 20)
	var inFlight, max atomic.Int32
	Map(items, limit, func(int) int {
		cur := inFlight.Add(1)
		for {
			m := max.Load()
			if cur <= m || max.CompareAndSwap(m, cur) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		inFlight.Add(-1)
		return 0
	})
	if got := max.Load(); got > limit {
		t.Fatalf("max in-flight = %d, want <= %d", got, limit)
	}
}

func TestMapLimitFallbacks(t *testing.T) {
	in := []int{1, 2, 3}
	// limit <= 0 and limit > len both mean one worker per item; order still holds.
	for _, lim := range []int{0, -1, 100} {
		got := Map(in, lim, func(n int) int { return n + 1 })
		if len(got) != 3 || got[0] != 2 || got[1] != 3 || got[2] != 4 {
			t.Fatalf("limit %d: got %v, want [2 3 4]", lim, got)
		}
	}
	var empty []int
	if got := Map(empty, 4, func(n int) int { return n }); len(got) != 0 {
		t.Fatalf("empty: got len %d, want 0", len(got))
	}
}
