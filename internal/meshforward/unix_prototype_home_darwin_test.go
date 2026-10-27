//go:build arm_forward_review && arm_forward_unix_prototype && darwin

package meshforward

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnixPrototypePhysicalHomeCaseAliasCapDarwin(t *testing.T) {
	home := unixPrototypeHome(t)
	alias := filepath.Join(filepath.Dir(home), strings.ToUpper(filepath.Base(home)))
	originalID, err := unixPrototypeNamedStat(home)
	if err != nil {
		t.Fatal(err)
	}
	aliasID, err := unixPrototypeNamedStat(alias)
	if err != nil || aliasID.dev != originalID.dev || aliasID.ino != originalID.ino || aliasID.uid != originalID.uid {
		t.Skip("this filesystem has no same-directory case alias")
	}
	canonical, err := filepath.EvalSymlinks(alias)
	if err != nil || canonical != alias {
		t.Skip("case alias is already normalized by path preflight")
	}
	unixPrototypeSlots.Lock()
	globalBefore := unixPrototypeSlots.allAdapters
	unixPrototypeSlots.Unlock()
	owners := make([]*UnixPrototype, 0, unixPrototypeMaxAdapters)
	for i := 0; i < unixPrototypeMaxAdapters; i++ {
		key := fmt.Sprintf("%032x", i+1)
		p, err := StartUnixPrototype(t.Context(), home, key, time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
			return nil, errors.New("unused")
		})
		if err != nil {
			t.Fatal("physical home did not admit its eight reviewed owners", i, err)
		}
		owners = append(owners, p)
		t.Cleanup(func() { _ = p.Close() })
	}
	physicalKey := owners[0].homeKey
	unixPrototypeSlots.Lock()
	beforeRefusal := unixPrototypeSlots.adapters[physicalKey]
	unixPrototypeSlots.Unlock()
	if beforeRefusal != unixPrototypeMaxAdapters {
		t.Fatal("literal path did not consume physical-home capacity", beforeRefusal)
	}
	refusedKey := strings.Repeat("f", 32)
	if p, err := StartUnixPrototype(t.Context(), alias, refusedKey, time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		return nil, nil
	}); p != nil || err == nil || !strings.Contains(err.Error(), "physical source home adapter capacity exhausted") {
		t.Fatal("case alias bypassed physical-home cap", p, err)
	}
	if _, err := os.Lstat(filepath.Join(home, "mesh", "forward-unix-"+refusedKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("alias refusal created an adapter directory", err)
	}
	unixPrototypeSlots.Lock()
	afterRefusal, globalAfterRefusal := unixPrototypeSlots.adapters[physicalKey], unixPrototypeSlots.allAdapters
	unixPrototypeSlots.Unlock()
	if afterRefusal != beforeRefusal || globalAfterRefusal != globalBefore+unixPrototypeMaxAdapters {
		t.Fatal("alias refusal changed charged identity counters", beforeRefusal, afterRefusal, globalBefore, globalAfterRefusal)
	}
	for _, p := range owners {
		if err := p.Close(); err != nil {
			t.Fatal("normal physical-home owner failed cleanup", err)
		}
	}
	unixPrototypeSlots.Lock()
	afterClose, globalAfterClose := unixPrototypeSlots.adapters[physicalKey], unixPrototypeSlots.allAdapters
	unixPrototypeSlots.Unlock()
	if afterClose != 0 || globalAfterClose != globalBefore {
		t.Fatal("physical-home owner counters did not return to baseline", afterClose, globalAfterClose, globalBefore)
	}
}
