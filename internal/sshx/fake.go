// SPDX-License-Identifier: AGPL-3.0-or-later

package sshx

import (
	"strings"
	"sync"
)

type Call struct {
	Target Target
	Cmd    []string
	Stdin  []byte
}

// Fake is a Runner test double: it records calls and returns canned responses.
type Fake struct {
	mu        sync.Mutex
	Calls     []Call
	Responses map[string]Result // keyed by Key(target, cmd); missing key -> zero Result, nil error
	Err       error             // if set, returned by every Run
}

// Run is safe for concurrent use: callers reconcile many nodes in parallel, so
// the call log and canned responses are guarded by a mutex.
func (f *Fake) Run(t Target, cmd []string, stdin []byte) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, Call{Target: t, Cmd: cmd, Stdin: stdin})
	return f.Responses[Key(t, cmd)], f.Err
}

// Key builds the Responses map key for a target + command. It folds in User and
// Identity so two Targets that share an Addr but differ by user or key do not
// collide.
func Key(t Target, cmd []string) string {
	return t.Addr + "|" + t.User + "|" + t.Identity + "|" + strings.Join(cmd, " ")
}
