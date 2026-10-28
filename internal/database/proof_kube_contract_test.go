package database_test

import (
	"strings"
	"testing"

	"getarm.org/internal/kubesource"
)

func TestKubernetesSourceContractStrictTextAndClosedValue(t *testing.T) {
	for _, mode := range []string{"canonical", "unknown-version", "duplicate-profile", "aliased-field", "extra-field", "blob"} {
		t.Run(mode, func(t *testing.T) {
			home := proofFixture(t, false)
			value := any(kubesource.ContractValue)
			switch mode {
			case "unknown-version":
				value = strings.Replace(kubesource.ContractValue, `"version":2`, `"version":3`, 1)
			case "duplicate-profile":
				value = strings.Replace(kubesource.ContractValue, `"profiles":[`, `"profiles":["t",`, 1)
			case "aliased-field":
				value = strings.Replace(kubesource.ContractValue, `"version"`, `"Version"`, 1)
			case "extra-field":
				value = strings.TrimSuffix(kubesource.ContractValue, "}") + `,"adopt_native_handles":true}`
			case "blob":
				value = []byte(kubesource.ContractValue)
			}
			edit(t, home, `INSERT INTO metadata(key,value) VALUES(?,?)`, kubesource.ContractKey, value)
			p, err := prove(t, home)
			if mode == "canonical" {
				if err != nil || !p.Complete || !p.Settled {
					t.Fatal(p, err)
				}
			} else if err == nil || p.Complete || p.Settled {
				t.Fatal("unsupported compatibility contract accepted", mode, p, err)
			}
		})
	}
}
