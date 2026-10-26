package syncservice

import (
	"bytes"
	"context"
	"testing"
)

func TestPreparationFootprintRetainsStructuralCharge(t *testing.T) {
	i, _, rows := intents(t)
	var wire bytes.Buffer
	if err := WritePreparation(&wire, Preparation{Version: Schema, Intent: i, Manifest: rows}); err != nil {
		t.Fatal(err)
	}
	encoded := append([]byte(nil), wire.Bytes()...)
	_, required, err := ReadPreparationWithFootprint(context.Background(), bytes.NewReader(encoded), testAdmission{bytes: 1 << 20, active: true})
	if err != nil {
		t.Fatal(err)
	}
	if required <= decodeReserveBytes+4*int64(len(encoded)-8) || required > 1<<20 {
		t.Fatal("decoded structure lost from handoff budget", required, len(encoded))
	}
	canonical, err := PreparationFootprint(context.Background(), Preparation{Version: Schema, Intent: i, Manifest: rows}, testAdmission{bytes: 1 << 20, active: true})
	if err != nil || canonical != required {
		t.Fatal("handoff lost original structural allowance", canonical, required, err)
	}
	if _, exact, err := ReadPreparationWithFootprint(context.Background(), bytes.NewReader(encoded), testAdmission{bytes: required, active: true}); err != nil || exact != required {
		t.Fatal("exact structural allowance changed", exact, required, err)
	}
	if _, _, err := ReadPreparationWithFootprint(context.Background(), bytes.NewReader(encoded), testAdmission{bytes: required - 1, active: true}); err == nil {
		t.Fatal("sub-footprint memory admitted")
	}
}
