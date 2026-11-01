package mesh

import "testing"

func TestWorkerRuntimeOwnerMetadataCannotIssueOrReplaceView(t *testing.T) {
	v := &WorkerRuntimeView{}
	v.self = v
	p := &WorkerInheritedImagePending{}
	p.self = p
	for _, view := range []*WorkerRuntimeView{nil, {}, v} {
		if view.RequireOriginalImageOwner(p) == nil {
			t.Fatal("unissued view accepted")
		}
	}
	copy := *v
	if copy.RequireOriginalImageOwner(p) == nil || p.view != nil || p.viewTried || p.inner != nil {
		t.Fatal("metadata check issued native/runtime custody")
	}
}
