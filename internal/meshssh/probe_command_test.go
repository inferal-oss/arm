package meshssh

import (
	"reflect"
	"strings"
	"testing"
)

func TestFixedReadOnlyProbePreservesOriginalCommandAndSentinel(t *testing.T) {
	for _, home := range []string{"", "/private/state", "/Users/person/Library/Application Support/arm", "/private/state with 'quotes'"} {
		for _, binary := range []string{"arm", "/private/selected-arm"} {
			got, err := FixedReadOnlyProbe(binary, home)
			if err != nil {
				t.Fatal(err)
			}
			before := []string{"/bin/sh", "-c", quote(probeScript), "arm-probe", quote(binary), quote(home)}
			if !reflect.DeepEqual(got.Remote, before) || !reflect.DeepEqual(got.Argv, []string{"/bin/sh", "-c", probeScript, "arm-probe", binary, home}) || got.Input != "ARM_READONLY_PROBE\n" {
				t.Fatalf("changed fixed probe: %#v", got)
			}
			got.Remote[0], got.Argv[1] = "forged", "forged"
			fresh, err := FixedReadOnlyProbe(binary, home)
			if err != nil || !reflect.DeepEqual(fresh.Remote, before) || fresh.Argv[1] != "-c" {
				t.Fatal("invocations share mutable state", fresh, err)
			}
		}
	}
}
func TestFixedReadOnlyProbeRefusesUnsafeSelectedArguments(t *testing.T) {
	for _, binary := range []string{"", "relative/arm", "/private/arm;id", "/private/with space", "/private/a'", "/private/a\n"} {
		if _, err := FixedReadOnlyProbe(binary, "/private/state"); err == nil {
			t.Fatal("unsafe binary accepted", binary)
		}
	}
	for _, home := range []string{"relative", "/private/../state", "/private/a\n", "/private/a\x00", "/" + strings.Repeat("a", 4096)} {
		if _, err := FixedReadOnlyProbe("arm", home); err == nil {
			t.Fatal("unsafe home accepted", home)
		}
	}
}
