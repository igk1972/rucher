// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build unix

package provision

import "syscall"

func flockEx(fd uintptr) error { return syscall.Flock(int(fd), syscall.LOCK_EX) }
func flockUn(fd uintptr) error { return syscall.Flock(int(fd), syscall.LOCK_UN) }
