package host

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestUserEnvArgv(t *testing.T) {
	got := userEnvArgv(1234, []string{"systemctl", "--user", "daemon-reload"})
	want := []string{
		"env",
		"XDG_RUNTIME_DIR=/run/user/1234",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1234/bus",
		"systemctl", "--user", "daemon-reload",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestFakeRecordsAndResponds(t *testing.T) {
	f := &Fake{Responses: map[string]Result{
		"user:1234:systemctl --user is-active web.service": {Stdout: "active", Code: 0},
	}}
	res, err := f.User("rucher-web", 1234, []string{"systemctl", "--user", "is-active", "web.service"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "active" {
		t.Fatalf("stdout = %q", res.Stdout)
	}
	if len(f.Calls) != 1 || f.Calls[0].User != "rucher-web" {
		t.Fatalf("calls = %+v", f.Calls)
	}
}

func TestNewExecCapturesExitCodeStdoutStderr(t *testing.T) {
	r := NewExec()
	res, err := r.Root([]string{"sh", "-c", "printf out; printf err 1>&2; exit 3"}, nil)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Code != 3 {
		t.Fatalf("Code = %d, want 3", res.Code)
	}
	if res.Stdout != "out" {
		t.Fatalf("Stdout = %q, want %q", res.Stdout, "out")
	}
	if res.Stderr != "err" {
		t.Fatalf("Stderr = %q, want %q", res.Stderr, "err")
	}
}

func TestNewExecFeedsStdin(t *testing.T) {
	r := NewExec()
	res, err := r.Root([]string{"cat"}, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "hello" {
		t.Fatalf("Stdout = %q, want %q", res.Stdout, "hello")
	}
	if res.Code != 0 {
		t.Fatalf("Code = %d, want 0", res.Code)
	}
}

func TestFakeRootAndErr(t *testing.T) {
	f := &Fake{Responses: map[string]Result{"root:id -u rucher-web": {Stdout: "1234"}}}
	res, err := f.Root([]string{"id", "-u", "rucher-web"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "1234" || len(f.Calls) != 1 || !f.Calls[0].Root {
		t.Fatalf("root call not recorded/keyed: %+v %+v", res, f.Calls)
	}
	f2 := &Fake{Err: errors.New("boom")}
	if _, err := f2.User("u", 1, []string{"x"}, nil); err == nil {
		t.Fatal("expected Fake.Err to be returned")
	}
}
