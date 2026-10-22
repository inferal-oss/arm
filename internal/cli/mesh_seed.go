package cli

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshssh"
)

// Only a fixed native verified-SSH helper accepts this framed stream. Neither
// browser nor peer RPC can upload catalog bytes or choose a helper/home.
func (a *app) meshProvisionSeed(args []string) (int, error) {
	if len(args) != 1 || len(args[0]) > 65536 {
		return 2, fmt.Errorf("one bounded native seed descriptor required")
	}
	raw, e := base64.RawStdEncoding.DecodeString(args[0])
	if e != nil || len(raw) > 32768 {
		return 2, fmt.Errorf("invalid bounded seed descriptor")
	}
	var q meshssh.CatalogSeedRequest
	if e = mesh.Decode(raw, &q); e != nil {
		return 2, e
	}
	if e = q.Validate(); e != nil {
		return 2, e
	}
	input := bufio.NewReader(a.in)
	gate, e := input.ReadSlice('\n')
	if e != nil || string(gate) != "ARM_REVIEWED_CATALOG_SEED_V1\n" || len(gate) > 128 {
		return 1, fmt.Errorf("reviewed catalog seed gate required; nothing written")
	}
	var proof mesh.RuntimeProof
	if e = client.Call(a.ctx, q.Home, "provision_runtime", nil, &proof); e != nil {
		return 1, e
	}
	if proof.Host != q.Host || proof.UID != q.Transfer.ExpectedUID || proof.SHA256 != q.Transfer.SHA256 || proof.Size != q.Transfer.Size || proof.Version != q.Transfer.Version || proof.Protocol != q.Transfer.Protocol {
		return 1, fmt.Errorf("started runtime changed; seed refused before writing")
	}
	receipt, e := mesh.SeedProvisionCatalog(a.ctx, q, io.Reader(input))
	if e != nil {
		return 1, e
	}
	return 0, a.print(receipt)
}
