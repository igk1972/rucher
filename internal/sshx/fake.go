package sshx

import "strings"

type Call struct {
	Target Target
	Cmd    []string
	Stdin  []byte
}

// Fake is a Runner test double: it records calls and returns canned responses.
type Fake struct {
	Calls     []Call
	Responses map[string]Result // keyed by Key(target, cmd); missing key -> zero Result, nil error
	Err       error             // if set, returned by every Run
}

func (f *Fake) Run(t Target, cmd []string, stdin []byte) (Result, error) {
	f.Calls = append(f.Calls, Call{Target: t, Cmd: cmd, Stdin: stdin})
	return f.Responses[Key(t, cmd)], f.Err
}

// Key builds the Responses map key for a target + command.
func Key(t Target, cmd []string) string {
	return t.Addr + "|" + strings.Join(cmd, " ")
}
