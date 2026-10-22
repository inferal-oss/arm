package database

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/cargoprofile"
	"github.com/yrashk/arm/internal/model"
)

func TestCargoResetProofProducerGraphRequiresIndependentReview(t *testing.T) {
	if e := checkCargoProofTypes(); e != nil {
		t.Fatal(e)
	}
	roots := []reflect.Type{reflect.TypeOf(cargoprofile.Prepared{}), reflect.TypeOf(cargoprofile.HelperRuntime{}), reflect.TypeOf(cargoprofile.HelperStarted{}), reflect.TypeOf(cargoprofile.HelperFinished{}), reflect.TypeOf(cargoprofile.BirthStep{}), reflect.TypeOf(model.CargoBinding{})}
	for name := range cargoProofTypeShapes {
		t.Run(name, func(t *testing.T) {
			shapes := map[string]string{}
			for k, v := range cargoProofTypeShapes {
				shapes[k] = v
			}
			shapes[name] += "\nFutureObligation bool `json:\"future_obligation\"`"
			if e := checkSSHProofTypes(shapes, roots...); e == nil || !strings.Contains(e.Error(), name) {
				t.Fatal("nested Cargo graph changed without explicit proof review", e)
			}
		})
	}
}
