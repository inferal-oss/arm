package database

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReviewForgetReceiptBudgetUsesDurableWriterEncoding(t *testing.T) {
	home := forgetFixture(t)
	review, err := ReviewForget(t.Context(), home, "receipt writer encoding boundary", false)
	if err != nil {
		t.Fatal(err)
	}
	defer review.Close()
	checked := 0
	for count := 350; count <= 700; count++ {
		plan := review.Plan
		plan.ForgetTrees = append([]ForgetTree{}, plan.ForgetTrees...)
		tree := plan.ForgetTrees[0]
		tree.Root = review.review.source.Directory.identity
		tree.Root.Name = tree.Name
		tree.Entries = nil
		prefix := ""
		for _, letter := range []string{"a", "b", "c", "d"} {
			name := strings.Repeat(letter, 128)
			id := tree.Root
			id.Name = name
			tree.Entries = append(tree.Entries, ForgetEntry{Path: prefix + name, File: id})
			prefix += name + "/"
		}
		for i := 0; i < count; i++ {
			name := strings.Repeat("x", 124) + fmt.Sprintf("%04d", i)
			id := plan.Files[0]
			id.Name = name
			id.Size = 1
			tree.Entries = append(tree.Entries, ForgetEntry{Path: prefix + name, File: id})
		}
		plan.ForgetTrees[0] = tree
		if err := validForgetTrees(plan); err != nil {
			t.Fatal(err)
		}
		// This is reachable progress: every file is erased while the quarantined
		// root is the final pending rmdir. Exercise the actual durable writer format.
		receipt := ResetReceipt{Schema: ResetSchema, Plan: plan, Phase: "erasing", Step: "forget:" + tree.Name + "/", Revision: 3000, Updated: plan.Created, SeedReady: true, Seed: plan.Files[0], Moves: map[string]string{}, Archives: map[string]string{}, Protected: plan.Namespaces, ForgetMoves: map[string]string{"dashboard-pending": "quarantined", "ssh-source-history": "absent"}, ForgetErased: map[string]bool{}}
		for _, f := range plan.Files {
			receipt.Moves[f.Name] = "erased"
		}
		for _, e := range tree.Entries {
			receipt.ForgetErased[tree.Name+"/"+e.Path] = true
		}
		raw, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if len(raw)+1 <= MaxRecordBytes {
			continue
		}
		staged := receipt
		staged.Phase, staged.Step, staged.Revision = "staged", "", 1
		staged.SeedReady, staged.Seed = false, File{}
		staged.ForgetErased = nil
		staged.Moves = map[string]string{}
		for _, f := range plan.Files {
			staged.Moves[f.Name] = "source"
		}
		staged.ForgetMoves = map[string]string{"dashboard-pending": "source", "ssh-source-history": "absent"}
		initial, err := json.MarshalIndent(staged, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if len(initial)+1 > MaxRecordBytes {
			continue
		}
		checked++
		t.Logf("%d entries: staged %d bytes fits, reachable late progress %d exceeds %d", count+4, len(initial)+1, len(raw)+1, MaxRecordBytes)
		if err := checkForgetReceiptBudget(plan); err == nil {
			t.Fatalf("accepted %d-entry plan needs %d durable receipt bytes after erasure, limit %d", count+4, len(raw)+1, MaxRecordBytes)
		}
		break
	}
	if checked == 0 {
		t.Fatal("fixture never exercised durable receipt boundary")
	}
}

func TestReviewForgetNativeInventoryRefusesLateReceiptOverflow(t *testing.T) {
	home := forgetFixture(t)
	pending := filepath.Join(home, "dashboard-pending")
	if err := os.Mkdir(pending, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending, ".lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	leaf := pending
	for _, letter := range []string{"a", "b", "c", "d"} {
		leaf = filepath.Join(leaf, strings.Repeat(letter, 128))
		if err := os.Mkdir(leaf, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 525; i++ {
		name := strings.Repeat("x", 124) + fmt.Sprintf("%04d", i)
		if err := os.WriteFile(filepath.Join(leaf, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := forgetSnapshot(t, home)
	review, err := ReviewForget(t.Context(), home, "native near-limit recovery inventory", false)
	if review != nil {
		review.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "recovery receipt") {
		t.Fatal("native inventory accepted despite later receipt encoding overflow", err)
	}
	if !reflect.DeepEqual(before, forgetSnapshot(t, home)) {
		t.Fatal("receipt preflight mutated original native inventory")
	}
}
