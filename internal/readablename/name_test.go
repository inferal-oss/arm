package readablename

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestV1VocabularyAndGoldenNames(t *testing.T) {
	sum := sha256.Sum256([]byte(vocabulary))
	if hex.EncodeToString(sum[:]) != "addd35536511597a02fa0a9ff1e5284677b8883b83e986e43f15a3db996b903e" {
		t.Fatal("v1 vocabulary changed")
	}
	if len(words) != 7776 {
		t.Fatal(len(words))
	}
	seen := map[string]bool{}
	safeWord := regexp.MustCompile(`^[a-z]+(?:-[a-z]+)*$`)
	for _, word := range words {
		if !safeWord.MatchString(word) || seen[word] {
			t.Fatalf("unsafe/duplicate word %q", word)
		}
		seen[word] = true
	}
	for _, tt := range []struct{ domain, id, want string }{
		{Host, "0123456789abcdef0123456789abcdef", "unlimited-obedient-crayfish-outburst"},
		{Lease, "0123456789abcdef0123456789abcdef", "sharper-snuggle-bonding-dandruff"},
		{Pool, "pool_0123456789abcdef0123456789abcdef", "shank-fable-subwoofer-maritime"},
		{Host, "node-雪", "circle-shrimp-actress-mug"},
		{"host\x00lease", "雪", "avenging-shudder-surreal-zap"},
	} {
		if got := FromID(tt.domain, tt.id); got != tt.want {
			t.Errorf("%q/%q = %q, want %q", tt.domain, tt.id, got, tt.want)
		}
	}
	if FromID(Host, "") != "" {
		t.Fatal("empty ID has a name")
	}
	if FromID("a", "b\x00c") == FromID("a\x00b", "c") {
		t.Fatal("domain separator injected")
	}
}

func TestExactAuthorizedResolutionAndRefusals(t *testing.T) {
	objects := []Candidate{{ID: "first", Name: "chosen"}, {ID: "second", Name: "first"}, {ID: "first", Name: "chosen"}}
	for _, target := range []string{"first", "chosen", FromID(Lease, "first")} {
		if id, err := Resolve(Lease, target, objects, true); id != "first" || err != nil {
			t.Fatal(id, err)
		}
	}
	for _, target := range []string{"", "cho", strings.ToUpper(FromID(Lease, "first")), FromID(Host, "first"), FromID(Lease, "private")} {
		if _, err := Resolve(Lease, target, objects, true); !errors.Is(err, ErrNotFound) {
			t.Fatal(target, err)
		}
	}
	for _, target := range []string{"first", "chosen", FromID(Lease, "first")} {
		if _, err := Resolve(Lease, target, objects, false); !errors.Is(err, ErrIncomplete) {
			t.Fatal(target, err)
		}
	}
	// A supplied name colliding with another object's derived label, duplicate
	// names and retained tombstones all require the original ID, never ordering.
	for _, objects := range [][]Candidate{
		{{ID: "first"}, {ID: "second", Name: FromID(Lease, "first")}},
		{{ID: "first", Name: "shared"}, {ID: "second", Name: "shared"}},
	} {
		target := objects[1].Name
		if _, err := Resolve(Lease, target, objects, true); !errors.Is(err, ErrAmbiguous) {
			t.Fatal(target, err)
		}
	}
}
