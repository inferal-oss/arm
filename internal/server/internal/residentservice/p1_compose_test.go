package residentservice

import (
	"os"
	"os/exec"
	"testing"
)

func TestP1CommandCompositionZeroAndUnfundedRefuse(t *testing.T) {
	command := &exec.Cmd{ExtraFiles: make([]*os.File, 13)}
	for _, group := range []*P1ServiceGroup{nil, {}} {
		if err := group.CheckOriginalCommand(command); err == nil {
			t.Fatal("zero group accepted command")
		}
	}
	if group, err := NewP1ServiceGroup(&ParentPending{}, nil, command); err == nil || group != nil {
		t.Fatal("unfunded group constructed")
	}
}
