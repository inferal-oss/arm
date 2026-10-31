package server

import "testing"

// PURE role/refusal controls only. The valid role is never executed: it would
// claim process originals and retain a live keeper, requiring native admission.
func TestResidentCoreRoleExactArgumentsAndPreclaimRefusal(t *testing.T) {
	if !originalResidentCoreArgs([]string{ResidentCoreRole}) {
		t.Fatal("exact private role refused by DATA parser")
	}
	for _, args := range [][]string{nil, {}, {"serve"}, {ResidentCoreRole + "-other"}, {ResidentCoreRole, "extra"}, {ResidentCoreRole, ResidentCoreRole}} {
		if originalResidentCoreArgs(args) {
			t.Fatal("nonexact role accepted")
		}
		if code := RunOriginalResidentCore(args); code != 1 {
			t.Fatal("pure rejection changed", code)
		}
	}
	if processResidentCoreEntry.call != nil || processResidentCoreEntry.root != nil {
		t.Fatal("pure arity rejection entered original custody")
	}
}
func TestResidentCoreEntryRejectsForeignOwnerWithoutClaimingOriginal(t *testing.T) {
	var foreign residentCoreEntry
	call := &residentCoreEntryCall{}
	if foreign.claimOriginal(call) || foreign.call != nil || processResidentCoreEntry.call != nil {
		t.Fatal("foreign entry acquired original role")
	}
	if processResidentCoreEntry.claimOriginal(nil) || processResidentCoreEntry.call != nil {
		t.Fatal("nil call acquired original role")
	}
}

func TestResidentCoreTimeoutRequiresActualPreparingRoot(t *testing.T) {
	root := &residentChildPending{}
	root.self = root
	if root.entry.ownsPreparingRoot(root) || processResidentCoreEntry.ownsPreparingRoot(root) {
		t.Fatal("unbound Root selected timeout effects")
	}
	foreign := &residentCoreEntry{root: root, call: &residentCoreEntryCall{initializationAttempted: true, initializationReturned: true, prepareAttempted: true}}
	root.entry = foreign
	if foreign.ownsPreparingRoot(root) {
		t.Fatal("foreign entry selected timeout effects")
	}
	root.entry = &processResidentCoreEntry
	if processResidentCoreEntry.ownsPreparingRoot(root) {
		t.Fatal("unclaimed static entry selected timeout effects")
	}
	if err := root.receiveOriginalBootstrap(nil); err != errResidentChildOriginal || root.bootstrapCall != nil {
		t.Fatal("nil context entered timeout constructor", err)
	}
}
