package policy

import (
	"strings"
	"testing"
)

func TestPrepareAndRevalidate(t *testing.T) {
	for _, command := range [][]string{{"pwd"}, {"pwd", "-P"}, {"uname", "-a"}, {"git", "status", "--short"}} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			inv, err := Prepare(command)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(inv.Executable, "/") || len(inv.SHA256) != 64 {
				t.Fatalf("invalid identity: %+v", inv)
			}
			if err := Revalidate(inv); err != nil {
				t.Fatal(err)
			}
			changed := inv
			changed.SHA256 = strings.Repeat("0", 64)
			if Revalidate(changed) == nil {
				t.Fatal("accepted changed hash")
			}
			changed = inv
			changed.Executable = "/tmp/" + strings.TrimPrefix(inv.Executable, "/")
			if Revalidate(changed) == nil {
				t.Fatal("accepted changed binary")
			}
			changed = inv
			changed.Args = append(append([]string{}, inv.Args...), "--help")
			if Revalidate(changed) == nil {
				t.Fatal("accepted extra argument")
			}
		})
	}
}

func TestRejectTemplates(t *testing.T) {
	for _, command := range [][]string{nil, {}, {"sh", "-c", "pwd"}, {"bash"}, {"rm", "-rf", "/"}, {"git", "status"}, {"git", "status", "--short", "--ignored"}, {"git", "-c", "core.fsmonitor=evil", "status", "--short"}, {"/usr/bin/pwd"}, {"pwd", "{input}"}, {"uname"}, {"pwd", "-L"}} {
		if _, err := Prepare(command); err == nil {
			t.Errorf("accepted %q", command)
		}
	}
	if Revalidate(Invocation{}) == nil {
		t.Fatal("accepted empty invocation")
	}
}

func TestValidateRequest(t *testing.T) {
	for _, request := range []string{"show git status", "what's my directory?", "describe this Linux system"} {
		if err := ValidateRequest(request); err != nil {
			t.Errorf("%q: %v", request, err)
		}
	}
	for _, request := range []string{"", "  ", strings.Repeat("x", 4097), "a\nb", "a\tb", "a\x00b", "a\x1bb", "a|b", "a;b", "a&b", "a>b", "a<b", "a`b`", "a$(pwd)", string([]byte{0xff})} {
		if ValidateRequest(request) == nil {
			t.Errorf("accepted %q", request)
		}
	}
}

func TestArgsAreNotShared(t *testing.T) {
	inv, err := Prepare([]string{"git", "status", "--short"})
	if err != nil {
		t.Fatal(err)
	}
	inv.Args[0] = "evil"
	next, err := Prepare([]string{"git", "status", "--short"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Args[0] != "--no-optional-locks" {
		t.Fatal("caller mutated policy")
	}
}
