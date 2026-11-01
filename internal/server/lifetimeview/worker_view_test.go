package lifetimeview_test

import (
	"reflect"
	"testing"

	"getarm.org/internal/server/lifetimeview"
)

func TestWorkerBorrowerAliasesKeepDistinctNonretiringTypes(t *testing.T) {
	pairs := [][2]reflect.Type{
		{reflect.TypeFor[*lifetimeview.WorkerManagerView](), reflect.TypeFor[*lifetimeview.ManagerView]()},
		{reflect.TypeFor[*lifetimeview.WorkerServerView](), reflect.TypeFor[*lifetimeview.ServerView]()},
		{reflect.TypeFor[*lifetimeview.WorkerManagerClaim](), reflect.TypeFor[*lifetimeview.ManagerClaim]()},
		{reflect.TypeFor[*lifetimeview.WorkerServerClaim](), reflect.TypeFor[*lifetimeview.ServerClaim]()},
	}
	for _, pair := range pairs {
		if pair[0] == pair[1] {
			t.Fatal("worker borrower aliases legacy authority")
		}
		for _, name := range []string{"Close", "Seal", "RetireOriginal", "OpenOriginal", "ManagerView", "ServerView"} {
			if _, found := pair[0].MethodByName(name); found {
				t.Fatal("borrower exposes native issuer/retirement authority", name)
			}
		}
		for field := range pair[0].Elem().NumField() {
			if pair[0].Elem().Field(field).IsExported() {
				t.Fatal("opaque borrower exposes constructor fields")
			}
		}
	}
}
