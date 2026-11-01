package serviceview_test

import (
	"reflect"
	"testing"

	"getarm.org/internal/server/serviceview"
)

func TestWorkerChargeAliasKeepsDistinctNonretiringAuthority(t *testing.T) {
	worker := reflect.TypeFor[*serviceview.WorkerManagerCharge]()
	if worker == reflect.TypeFor[*serviceview.ManagerCharge]() {
		t.Fatal("worker standing charge aliases legacy authority")
	}
	for _, name := range []string{"Close", "Release", "End", "RetireOriginal", "PrepareOriginalDebit"} {
		if _, found := worker.MethodByName(name); found {
			t.Fatal("charge exposes issuer or retirement authority", name)
		}
	}
	for field := range worker.Elem().NumField() {
		if worker.Elem().Field(field).IsExported() {
			t.Fatal("opaque charge exposes constructor fields")
		}
	}
	var zero serviceview.WorkerManagerCharge
	if _, err := zero.ObserveOriginal(); err == nil {
		t.Fatal("zero charge supplied an original observation")
	}
}
