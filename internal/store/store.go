// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store fetches all nodes' desired state into a local checkout.
package store

import "context"

// Store returns a local checkout path and the fetched revision.
type Store interface {
	Sync(ctx context.Context) (checkout string, revision string, err error)
}
