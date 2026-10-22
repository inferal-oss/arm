package cargoprofile

import (
	"strings"
	"testing"
)

func TestMetadataEvidenceRejectsAmbiguousAndUncontrolledOutputs(t *testing.T) {
	roots := Roots{Target: "/managed/target", Build: "/managed/build", Control: "/managed/control"}
	good := `{"workspace_root":"/project","target_directory":"/managed/target","build_directory":"/managed/build","future":{"ok":[1,true]}}`
	if _, e := DecodeMetadata([]byte(good), roots); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{strings.Replace(good, `"build_directory":"/managed/build"`, `"build_directory":null`, 1), strings.Replace(good, `"build_directory":"/managed/build"`, `"Build_directory":"/managed/build"`, 1), strings.Replace(good, `"/managed/build"`, `"/unowned"`, 1), strings.Replace(good, `"workspace_root":"/project"`, `"workspace_root":"/project","workspace_root":"/other"`, 1), good + ` {}`, string([]byte{'"', 0xff, '"'}), `[]`} {
		if _, e := DecodeMetadata([]byte(raw), roots); e == nil {
			t.Fatal("unsafe metadata accepted", raw)
		}
	}
}
