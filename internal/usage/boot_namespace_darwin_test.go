package usage

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/model"
)

func TestDarwinBootNamespaceRequiresExactKernelUUID(t *testing.T) {
	const valid = "12345678-9ABC-4DEF-8123-456789ABCDEF\x00"
	for _, tc := range []struct {
		name string
		raw  string
		size uintptr
		err  error
	}{
		{"unavailable", valid, 37, errors.New("sysctl refused")},
		{"empty", "", 0, nil},
		{"short", valid, 36, nil},
		{"oversize", valid, 38, nil},
		{"unterminated", valid[:36] + "X", 37, nil},
		{"embedded-nul", "\x00" + valid[1:], 37, nil},
		{"lowercase", "abcdefab" + valid[8:], 37, nil},
		{"nonhex", "G" + valid[1:], 37, nil},
		{"separator", valid[:8] + "_" + valid[9:], 37, nil},
		{"nil", "00000000-0000-0000-0000-000000000000\x00", 37, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns, err := darwinBootNamespace(func(raw *[darwinBootUUIDSize]byte, size *uintptr) error {
				if *size != 37 {
					t.Fatal("read was not bounded to uuid_string_t")
				}
				copy(raw[:], tc.raw)
				*size = tc.size
				return tc.err
			})
			if err == nil || ns != "" {
				t.Fatalf("invalid boot identity accepted: %q, %v", ns, err)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatal("lost sysctl failure", err)
			}
		})
	}
	read := func(value string) string {
		ns, err := darwinBootNamespace(func(raw *[darwinBootUUIDSize]byte, size *uintptr) error { copy(raw[:], value); return nil })
		if err != nil {
			t.Fatal(err)
		}
		return ns
	}
	first := read(valid)
	if first != "darwin:bootsessionuuid:"+valid[:36] || read("2"+valid[1:]) == first {
		t.Fatal("boot sessions did not retain distinct exact identities")
	}
}

// Each exec initializes its own native cache. Observe the same parent through
// libproc from both processes, rather than comparing values from one cache.
func TestNativeDarwinNamespaceAcrossIndependentProcesses(t *testing.T) {
	parent, err := Capture(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(parent.Namespace, "darwin:bootsessionuuid:") || len(parent.Namespace) != len("darwin:bootsessionuuid:")+36 {
		t.Fatal("native capture did not use the exact boot-session UUID namespace", parent.Namespace)
	}
	for range 3 {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestDarwinNamespaceChild$")
		cmd.Env = append(os.Environ(), "ARM_DARWIN_NAMESPACE_CHILD=1")
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("independent capture: %v %s", err, raw)
		}
		var got [2]model.ProcessIdentity
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err, string(raw))
		}
		if got[0] != parent || got[1].Namespace != parent.Namespace {
			t.Fatalf("independent process identity differs: parent=%+v child=%+v", parent, got)
		}
	}
}

func TestDarwinNamespaceChild(t *testing.T) {
	if os.Getenv("ARM_DARWIN_NAMESPACE_CHILD") != "1" {
		t.Skip("independent process fixture")
	}
	parent, err := Capture(os.Getppid())
	if err != nil {
		t.Fatal(err)
	}
	self, err := Capture(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode([2]model.ProcessIdentity{parent, self}); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
