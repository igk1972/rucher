// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build !unix

package provision

// Node-side mutation only runs on Linux; on other platforms the lock is a no-op so
// the operator-side build (e.g. windows) still compiles.
func flockEx(fd uintptr) error { return nil }
func flockUn(fd uintptr) error { return nil }
