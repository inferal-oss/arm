package model

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestSSHControlCommitmentPreservesExactCountAndIdentity(t *testing.T) {
	c := SSHControlCommitment{Schema: 1, Source: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Pair: strings.Repeat("c", 64), Revision: 1, Certificate: strings.Repeat("d", 64), Owner: "501", Account: "fixture", HostKeySHA256: strings.Repeat("e", 64), Key: "exact", PublicKeySHA256: strings.Repeat("f", 64), Reason: "exact control", TTL: time.Minute, Resources: Resources{CPU: .5, Memory: math.MaxInt64, Disk: math.MaxInt64 - 1}, CWD: "/tmp", Kind: "exec", CommandSHA256: strings.Repeat("1", 64)}
	digest, e := c.Digest()
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	var decoded SSHControlCommitment
	if e = json.Unmarshal(raw, &decoded); e != nil {
		t.Fatal(e)
	}
	after, e := decoded.Digest()
	if e != nil || after != digest || decoded.Resources.Memory != math.MaxInt64 || decoded.Resources.Disk != math.MaxInt64-1 {
		t.Fatal("int64 quantity changed", e)
	}
	for _, mutate := range []func(*SSHControlCommitment){func(v *SSHControlCommitment) { v.Resources.Disk-- }, func(v *SSHControlCommitment) { v.PTY = true }, func(v *SSHControlCommitment) { v.Owner = "502" }, func(v *SSHControlCommitment) { v.HostKeySHA256 = strings.Repeat("2", 64) }} {
		altered := c
		mutate(&altered)
		d, e := altered.Digest()
		if e != nil || d == digest {
			t.Fatal("control authority or effect not committed", e)
		}
	}
	for _, uid := range []string{"01", "-1", "4294967296", "name"} {
		bad := c
		bad.Owner = uid
		if bad.Validate() == nil {
			t.Fatal("noncanonical UID accepted", uid)
		}
	}
}
