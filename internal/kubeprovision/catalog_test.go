package kubeprovision

import "testing"

func TestOwnedCatalogProofAndStartCommandsAreExact(t *testing.T) {
	p, plan, _ := fixture(t)
	record, err := p.Apply(t.Context(), plan)
	if err != nil || record.Pod == nil {
		t.Fatalf("fixture Pod: %v", err)
	}
	proof, err := fixedExecCommand(plan, *record.Pod, execCatalogProof)
	if err != nil || proof[2] != catalogProofScript {
		t.Fatalf("fixed catalog proof: %v", err)
	}
	if kind, err := classifyReviewedExec(plan, *record.Pod, proof, nil); err != nil || kind != execCatalogProof {
		t.Fatalf("catalog proof rejected: %s %v", kind, err)
	}
	start, err := fixedExecCommand(plan, *record.Pod, execStart)
	if err != nil || start[len(start)-1] != plan.Review.CatalogSHA256 {
		t.Fatalf("start omitted exact catalog: %v", err)
	}
	changed := append([]string(nil), start...)
	changed[len(changed)-1] = hash("other-catalog")
	if _, err := classifyReviewedExec(plan, *record.Pod, changed, nil); err == nil {
		t.Fatal("changed catalog admitted to owned start helper")
	}
	runtimeCommand, err := fixedExecCommand(plan, *record.Pod, execRuntimeProof)
	if err != nil || runtimeCommand[2] != runtimeProofScript {
		t.Fatalf("fixed runtime proof: %v", err)
	}
	if kind, err := classifyReviewedExec(plan, *record.Pod, runtimeCommand, nil); err != nil || kind != execRuntimeProof {
		t.Fatalf("runtime proof rejected: %s %v", kind, err)
	}
}
