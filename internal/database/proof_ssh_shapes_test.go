package database

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/model"
)

func TestSSHProofProducerShapesRequireIndependentReview(t *testing.T) {
	if e := checkSSHProofTypeShapes(); e != nil {
		t.Fatal(e)
	}
	// Simulate producer/proof declaration drift at each nested boundary. The
	// independent literal schema cannot expand merely because model expanded.
	for _, name := range []string{"model.Resources", "model.ProcessIdentity", "model.SSHLookup", "model.SSHOperation", "model.SSHExecutionRequest", "model.Lease", "model.Request", "model.WorkloadUsage", "model.ProcessUsage", "model.CargoBinding"} {
		t.Run(name, func(t *testing.T) {
			declared := map[string]string{}
			for k, v := range sshProofTypeShapes {
				declared[k] = v
			}
			declared[name] += "\nFutureField string `json:\"future_field,omitempty\"`"
			if e := checkSSHProofTypes(declared, reflect.TypeOf(model.Resources{}), reflect.TypeOf(model.ProcessIdentity{}), reflect.TypeOf(model.SSHLookup{})); e == nil || !strings.Contains(e.Error(), name) {
				t.Fatal("nested producer drift silently accepted", e)
			}
		})
	}
	// Removing a required current node from the frozen declaration catalog must
	// also fail even when that optional receipt field is absent in raw JSON.
	declared := map[string]string{}
	for k, v := range sshProofTypeShapes {
		declared[k] = v
	}
	delete(declared, "model.WorkloadUsage")
	if e := checkSSHProofTypes(declared, reflect.TypeOf(model.SSHLookup{})); e == nil {
		t.Fatal("missing nested declaration accepted")
	}
}
