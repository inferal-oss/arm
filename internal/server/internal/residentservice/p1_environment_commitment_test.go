package residentservice

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestP1EnvironmentCommitmentDataUsesExactCanonicalProfile(t *testing.T) {
	environment := &ExecutionEnvironment{Schema: 1, Entries: []string{"GOMAXPROCS=1", "GOMEMLIMIT=1B"}}
	got, err := p1EnvironmentSHA256Data(environment)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(`{"schema":1,"entries":["GOMAXPROCS=1","GOMEMLIMIT=1B"]}`))
	if got != hex.EncodeToString(h[:]) {
		t.Fatal("profile canonical representation changed")
	}
	environment.Entries[0] = "GOMAXPROCS=2"
	changed, err := p1EnvironmentSHA256Data(environment)
	if err != nil || changed == got {
		t.Fatal("environment not committed", err)
	}
	if _, err := p1EnvironmentSHA256Data(nil); err != ErrInvalid {
		t.Fatal(err)
	}
	// A bounded legal profile can still expand beyond prepared canonical Env's
	// 8KiB JSON limit. Reject that representation before debit as well.
	large := &ExecutionEnvironment{Schema: 1, Entries: []string{"GOMAXPROCS=1", "GOMEMLIMIT=1B", "PATH=/" + strings.Repeat("<", 2000)}}
	if _, err := large.Validate(); err != nil {
		t.Fatal("synthetic legal Env DATA", err)
	}
	if _, err := p1EnvironmentSHA256Data(large); err != ErrInvalid {
		t.Fatal("canonical expansion overflow accepted", err)
	}
}
