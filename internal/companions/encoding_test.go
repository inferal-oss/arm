package companions

import (
	"bytes"
	"io"
	"testing"
)

func TestFramesPreserveExactCanonicalCompressedPayloads(t *testing.T) {
	files, m := fixture(t)
	catalog, e := Load(files, m.Version, m.Protocol)
	if e != nil {
		t.Fatal(e)
	}
	var want []byte
	for _, p := range m.Artifacts {
		want = append(want, files["generated/"+p.Name+".gz"].Data...)
	}
	frames := catalog.Frames()
	got, e := io.ReadAll(frames)
	ce := frames.Close()
	if e != nil || ce != nil || !bytes.Equal(got, want) {
		t.Fatal("frames changed catalog bytes", e, ce)
	}
	first := files["generated/"+m.Artifacts[0].Name+".gz"]
	first.Data[0] ^= 1
	frames = catalog.Frames()
	_, e = io.Copy(io.Discard, frames)
	frames.Close()
	if e == nil {
		t.Fatal("changed compressed source accepted")
	}
}
