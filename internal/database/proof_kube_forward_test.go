package database

import (
	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
	"strings"
	"testing"
)

func forwardProofTriple() (model.Lease, model.Lease, model.Lease) {
	source, disk := sourceProofPair()
	s := kubesource.Scope{Operation: strings.Repeat("a", 64), Attempt: strings.Repeat("b", 32), Stage: kubesource.Forward, B: 1}
	source.Request = s.Request(source.Host, "", false)
	source.Request.Kind = "relay-transit"
	source.Request.StorageLease = disk.ID
	disk.Request = s.Request(source.Host, "", true)
	port := model.Lease{ID: strings.Repeat("3", 32), Host: source.Host, Owner: source.Owner, State: "active", Request: s.PortRequest(source.Host, "", "host")}
	binding := port.Request.Ports[0]
	binding.Port = 23456
	port.Ports = []model.PortBinding{{PortClaim: binding, State: "held"}}
	source.Request.PortLease = port.ID
	return source, disk, port
}
func TestKubernetesForwardProofExactTripleAndForeignReferences(t *testing.T) {
	for _, mode := range []string{"complete", "missing-port", "foreign-port-consumer", "storage-as-port", "port-as-storage", "source-as-storage", "source-as-port", "empty-partial-port", "foreign-partial-port"} {
		t.Run(mode, func(t *testing.T) {
			source, disk, port := forwardProofTriple()
			rows := map[string]model.Lease{source.ID: source, disk.ID: disk, port.ID: port}
			switch mode {
			case "missing-port":
				delete(rows, port.ID)
			case "foreign-port-consumer":
				rows["foreign"] = model.Lease{Request: model.Request{PortLease: port.ID}}
			case "storage-as-port":
				rows["foreign"] = model.Lease{Request: model.Request{PortLease: disk.ID}}
			case "port-as-storage":
				rows["foreign"] = model.Lease{Request: model.Request{StorageLease: port.ID}}
			case "source-as-storage":
				rows["foreign"] = model.Lease{Request: model.Request{StorageLease: source.ID}}
			case "source-as-port":
				rows["foreign"] = model.Lease{Request: model.Request{PortLease: source.ID}}
			case "empty-partial-port", "foreign-partial-port":
				source.Request.PortLease = ""
				rows[source.ID] = source
				if mode == "foreign-partial-port" {
					rows["foreign"] = model.Lease{State: "terminal", Request: model.Request{PortLease: port.ID}}
				}
			}
			err := validateKubernetesSourcePairs(rows)
			valid := mode == "complete" || mode == "empty-partial-port"
			if (err == nil) != valid {
				t.Fatal(mode, err)
			}
		})
	}
}
