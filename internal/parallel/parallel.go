// SPDX-License-Identifier: AGPL-3.0-or-later

// Package parallel runs a bounded number of independent work functions
// concurrently, preserving input order in the result slice.
package parallel

import "sync"

// Map applies fn to each item with at most limit concurrent calls and returns
// the results in input order. A limit <= 0 or greater than len(items) runs one
// worker per item. fn must be safe to call concurrently and must not return an
// error — per-item failure belongs in R.
func Map[T, R any](items []T, limit int, fn func(T) R) []R {
	out := make([]R, len(items))
	if len(items) == 0 {
		return out
	}
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	// Acquire a slot before spawning so at most `limit` goroutines run — and are
	// created — at once. Results go to disjoint out[i], so no lock is needed.
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = fn(items[i])
		}()
	}
	wg.Wait()
	return out
}
