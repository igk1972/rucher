package node

import (
	"fmt"
	"strings"
)

type Call struct {
	Root  bool
	User  string
	UID   int
	Argv  []string
	Stdin []byte
}

// Fake is a Runner test double: it records calls and returns canned responses.
type Fake struct {
	Calls     []Call
	Responses map[string]Result // keyed by key(); missing key -> zero Result, nil error
	Err       error             // if set, returned by every call
}

func (f *Fake) Root(argv []string, stdin []byte) (Result, error) {
	f.Calls = append(f.Calls, Call{Root: true, Argv: argv, Stdin: stdin})
	return f.Responses["root:"+strings.Join(argv, " ")], f.Err
}

func (f *Fake) User(user string, uid int, argv []string, stdin []byte) (Result, error) {
	f.Calls = append(f.Calls, Call{User: user, UID: uid, Argv: argv, Stdin: stdin})
	k := fmt.Sprintf("user:%d:%s", uid, strings.Join(argv, " "))
	return f.Responses[k], f.Err
}
