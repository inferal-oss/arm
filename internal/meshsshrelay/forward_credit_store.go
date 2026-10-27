package meshsshrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/inferal-oss/arm/internal/meshforward"
	"github.com/inferal-oss/arm/internal/workspace"
	"golang.org/x/sys/unix"
)

const (
	forwardCreditName       = "forward-credit.json"
	maxForwardCreditBytes   = 256 << 10
	maxForwardCreditEntries = 256
	maxForwardCreditSpend   = 64 << 10
)

func lockForwardPolicy(ctx context.Context, d *os.File, mode int) (int, error) {
	if ctx == nil || mode != unix.LOCK_SH && mode != unix.LOCK_EX {
		return -1, errors.New("invalid TCP credit policy lock request")
	}
	fd, err := unix.Openat(int(d.Fd()), policyLockName, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return -1, err
	}
	for {
		if err = ctx.Err(); err != nil {
			unix.Close(fd)
			return -1, err
		}
		err = unix.Flock(fd, mode|unix.LOCK_NB)
		if !errors.Is(err, unix.EWOULDBLOCK) {
			break
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			unix.Close(fd)
			return -1, ctx.Err()
		case <-timer.C:
		}
	}
	if err != nil {
		unix.Close(fd)
		return -1, err
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&077 != 0 ||
		st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) {
		unix.Close(fd)
		return -1, errors.New("TCP credit policy lock authority differs")
	}
	if err = ctx.Err(); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

// This owner-private journal retains every credit epoch, including revoked
// grants. Its bounded capacity fails closed rather than resetting old credit.
type forwardCreditLedger struct {
	Schema   int                  `json:"schema"`
	Local    string               `json:"local"`
	LocalPin string               `json:"local_pin"`
	ID       string               `json:"id"`
	Entries  []forwardCreditEntry `json:"entries"`
}

type forwardCreditEntry struct {
	Nonce        string `json:"nonce"`
	Digest       string `json:"grant_sha256"`
	ReviewSHA256 string `json:"review_sha256"`
	Total        int64  `json:"total_bytes"`
	Spent        int64  `json:"spent_bytes"`
}

func (l forwardCreditLedger) validate(local, pin, id string) error {
	if l.Schema != 1 || l.Local != local || l.LocalPin != pin || l.ID != id ||
		!hexBytes(l.Local, 16) || !hexBytes(l.LocalPin, 32) || !hexBytes(l.ID, 16) ||
		len(l.Entries) > maxForwardCreditEntries {
		return errors.New("TCP credit ledger identity or capacity differs")
	}
	seen := make(map[string]bool, len(l.Entries))
	for _, e := range l.Entries {
		if !hexBytes(e.Nonce, 16) || !hexBytes(e.Digest, 32) || !hexBytes(e.ReviewSHA256, 32) ||
			e.Total < 1 || e.Total > meshforward.MaxGrantBytes || e.Spent < 0 || e.Spent > e.Total || seen[e.Nonce] {
			return errors.New("TCP credit ledger record invalid or duplicated")
		}
		seen[e.Nonce] = true
	}
	raw, err := json.Marshal(l)
	if err != nil || len(raw)+1 > maxForwardCreditBytes {
		return errors.New("TCP credit ledger exceeds byte bound")
	}
	return nil
}

func readForwardCreditAt(d *os.File) (forwardCreditLedger, bool, error) {
	fd, err := unix.Openat(int(d.Fd()), forwardCreditName, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return forwardCreditLedger{}, false, nil
	}
	if err != nil {
		return forwardCreditLedger{}, false, err
	}
	f := os.NewFile(uintptr(fd), forwardCreditName)
	defer f.Close()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return forwardCreditLedger{}, false, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&077 != 0 || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) ||
		st.Size < 1 || st.Size > maxForwardCreditBytes {
		return forwardCreditLedger{}, false, errors.New("TCP credit ledger ownership, type or bound differs")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxForwardCreditBytes+1))
	if err != nil || len(raw) != int(st.Size) {
		return forwardCreditLedger{}, false, errors.New("TCP credit ledger changed while reading")
	}
	var after unix.Stat_t
	if err = unix.Fstat(fd, &after); err != nil || after.Dev != st.Dev || after.Ino != st.Ino || after.Size != st.Size || after.Mtim != st.Mtim {
		return forwardCreditLedger{}, false, errors.New("TCP credit ledger changed while reading")
	}
	var ledger forwardCreditLedger
	if err = workspace.Decode(bytes.TrimSpace(raw), &ledger); err != nil {
		return forwardCreditLedger{}, false, err
	}
	return ledger, true, nil
}

// writeForwardCreditAt replaces the journal only after its new image is synced.
// An error after rename is deliberately ambiguous and never permits a write.
func writeForwardCreditAt(d *os.File, ledger forwardCreditLedger) error {
	return writeForwardCreditAtWithFault(d, ledger, nil)
}

// afterRename is test-only fault injection for an ambiguous directory sync.
func writeForwardCreditAtWithFault(d *os.File, ledger forwardCreditLedger, afterRename func() error) error {
	if err := ledger.validate(ledger.Local, ledger.LocalPin, ledger.ID); err != nil {
		return err
	}
	raw, err := json.Marshal(ledger)
	if err != nil {
		return err
	}
	name := forwardCreditName + "." + strconv.FormatInt(time.Now().UnixNano(), 10) + ".tmp"
	fd, err := unix.Openat(int(d.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	defer unix.Unlinkat(int(d.Fd()), name, 0)
	if _, err = f.Write(append(raw, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = unix.Renameat(int(d.Fd()), name, int(d.Fd()), forwardCreditName); err != nil {
		return err
	}
	if afterRename != nil {
		if err = afterRename(); err != nil {
			return fmt.Errorf("TCP credit ledger rename occurred but directory sync is unconfirmed: %w", err)
		}
	}
	if err = unix.Fsync(int(d.Fd())); err != nil {
		return fmt.Errorf("TCP credit ledger rename occurred but directory sync is unconfirmed: %w", err)
	}
	return nil
}

// prepareForwardCreditsAt is called under the same exclusive policy lock as
// CommitPolicy, before publishing the new grant. A missing ledger is created
// only as an effect of the first positive reviewed grant commit.
func prepareForwardCreditsAt(d *os.File, before, after Policy) error {
	old := make(map[string]meshforward.Grant, len(before.Forwards))
	for _, grant := range before.Forwards {
		if grant.CreditNonce != "" {
			old[grant.CreditNonce] = grant
		}
	}
	var fresh []meshforward.Grant
	for _, grant := range after.Forwards {
		if grant.CreditNonce == "" {
			continue
		}
		prior, exists := old[grant.CreditNonce]
		if exists && !reflect.DeepEqual(prior, grant) {
			return errors.New("TCP credit nonce reused for changed grant")
		}
		if !exists {
			fresh = append(fresh, grant)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	if after.CreditLedgerID == "" {
		return errors.New("reviewed TCP credit ledger identity required")
	}
	ledger, exists, err := readForwardCreditAt(d)
	if err != nil {
		return err
	}
	if !exists {
		if before.CreditLedgerID != "" {
			return errors.New("existing TCP credit ledger is missing")
		}
		ledger = forwardCreditLedger{Schema: 1, Local: after.Local, LocalPin: after.LocalPin, ID: after.CreditLedgerID}
	} else if err = ledger.validate(after.Local, after.LocalPin, after.CreditLedgerID); err != nil {
		return err
	}
	used := make(map[string]forwardCreditEntry, len(ledger.Entries))
	for _, e := range ledger.Entries {
		used[e.Nonce] = e
	}
	reviewDigest := PolicyDigest(after)
	changed := false
	for _, grant := range fresh {
		if err = grant.ValidateCredit(); err != nil {
			return err
		}
		digest, e := grant.Digest()
		if e != nil {
			return e
		}
		if prior, exists := used[grant.CreditNonce]; exists {
			// A retry of the exact original CAS review may finish publishing an
			// already prepared record. Keep its existing spent balance.
			if prior.Digest != digest || prior.Total != grant.TotalBytes || prior.ReviewSHA256 != reviewDigest {
				return errors.New("TCP credit nonce already belongs to another reviewed history")
			}
			continue
		}
		if len(ledger.Entries) >= maxForwardCreditEntries {
			return errors.New("TCP credit ledger capacity reached")
		}
		entry := forwardCreditEntry{Nonce: grant.CreditNonce, Digest: digest, ReviewSHA256: reviewDigest, Total: grant.TotalBytes}
		ledger.Entries = append(ledger.Entries, entry)
		used[grant.CreditNonce] = entry
		changed = true
	}
	if !changed {
		return nil
	}
	return writeForwardCreditAt(d, ledger)
}

// CheckForwardCredit denies READY when the durable credit record is absent,
// corrupt, mismatched or already exhausted. Each write has its own reservation.
func CheckForwardCredit(ctx context.Context, home, local, localPin string, grant meshforward.Grant) error {
	if ctx == nil || grant.ValidateCredit() != nil {
		return errors.New("invalid TCP credit check")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d, err := policyDirectory(home)
	if err != nil {
		return err
	}
	defer d.Close()
	fd, err := lockForwardPolicy(ctx, d, unix.LOCK_SH)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	policy, err := readPolicyAt(d, local, localPin)
	if err != nil {
		return err
	}
	if ctx.Err() != nil || policy.CreditLedgerID == "" || !time.Now().Before(grant.Expires) {
		return errors.New("TCP grant expired or credit ledger unavailable")
	}
	digest, err := grant.Digest()
	if err != nil {
		return err
	}
	current := false
	for _, g := range policy.Forwards {
		storedDigest, e := g.Digest()
		if e == nil && storedDigest == digest {
			current = true
			break
		}
	}
	if !current {
		return errors.New("TCP grant changed before credit check")
	}
	ledger, exists, err := readForwardCreditAt(d)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("TCP credit ledger missing; no balance may be inferred")
	}
	if err = ledger.validate(local, localPin, policy.CreditLedgerID); err != nil {
		return err
	}
	for _, e := range ledger.Entries {
		if e.Nonce == grant.CreditNonce {
			if e.Digest != digest || e.Total != grant.TotalBytes || e.Spent >= e.Total {
				return errors.New("TCP grant credit exhausted or history differs")
			}
			return nil
		}
	}
	return errors.New("TCP grant credit epoch missing from durable ledger")
}

// ReserveForwardBytes durably prepays application bytes before one write. It
// serializes with grant changes and other writers across daemon processes.
// There is no refund for short writes, cancellation, or process death.
func ReserveForwardBytes(ctx context.Context, home, local, localPin string, grant meshforward.Grant, n int) (int, error) {
	return reserveForwardBytesWithWrite(ctx, home, local, localPin, grant, n, writeForwardCreditAt)
}

func reserveForwardBytesWithWrite(ctx context.Context, home, local, localPin string, grant meshforward.Grant, n int,
	write func(*os.File, forwardCreditLedger) error) (int, error) {
	if ctx == nil || write == nil || n < 1 || n > maxForwardCreditSpend || grant.ValidateCredit() != nil {
		return 0, errors.New("invalid TCP credit reservation")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	d, err := policyDirectory(home)
	if err != nil {
		return 0, err
	}
	defer d.Close()
	fd, err := lockForwardPolicy(ctx, d, unix.LOCK_EX)
	if err != nil {
		return 0, err
	}
	defer unix.Close(fd)
	policy, err := readPolicyAt(d, local, localPin)
	if err != nil {
		return 0, err
	}
	if policy.CreditLedgerID == "" || !time.Now().Before(grant.Expires) || ctx.Err() != nil {
		return 0, errors.New("TCP grant expired or credit ledger unavailable")
	}
	digest, err := grant.Digest()
	if err != nil {
		return 0, err
	}
	current := false
	for _, g := range policy.Forwards {
		storedDigest, e := g.Digest()
		if e == nil && storedDigest == digest {
			current = true
			break
		}
	}
	if !current {
		return 0, errors.New("TCP grant changed before byte reservation")
	}
	ledger, exists, err := readForwardCreditAt(d)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, errors.New("TCP credit ledger missing; no balance may be inferred")
	}
	if err = ledger.validate(local, localPin, policy.CreditLedgerID); err != nil {
		return 0, err
	}
	for i := range ledger.Entries {
		e := &ledger.Entries[i]
		if e.Nonce != grant.CreditNonce {
			continue
		}
		if e.Digest != digest || e.Total != grant.TotalBytes || e.Spent >= e.Total {
			return 0, errors.New("TCP grant byte credit exhausted or history differs")
		}
		granted := int(min(int64(n), e.Total-e.Spent))
		e.Spent += int64(granted)
		if err = ctx.Err(); err != nil {
			return 0, err
		}
		if err = write(d, ledger); err != nil {
			return 0, err
		}
		return granted, nil
	}
	return 0, errors.New("TCP grant credit epoch missing from durable ledger")
}
