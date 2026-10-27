package mesh

import (
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"path/filepath"
	"reflect"
	"strconv"

	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/meshssh"
)

type CatalogSeedReceipt struct {
	Request     meshssh.CatalogSeedRequest `json:"request"`
	Destination string                     `json:"destination"`
	Identity    *CatalogIdentity           `json:"identity,omitempty"`
}

func SeedProvisionCatalog(ctx context.Context, q meshssh.CatalogSeedRequest, input io.Reader) (ProvisionReceipt, error) {
	var receipt ProvisionReceipt
	if e := q.Validate(); e != nil {
		return receipt, e
	}
	_, unlock, e := provisionLock(q.Transfer)
	if e != nil {
		return receipt, e
	}
	defer unlock()
	receipt, e = LookupProvision(q.Transfer)
	if e != nil {
		return receipt, e
	}
	if receipt.Phase != "started_verified" || receipt.CatalogSeed != nil || receipt.StartHome != q.Home || receipt.StartedHost != q.Host {
		return receipt, fmt.Errorf("seed already attempted or changed exact started authority; lookup only, no resend")
	}
	receipt.Phase = "seed_intent"
	receipt.CatalogSeed = &CatalogSeedReceipt{Request: q, Destination: filepath.Join(q.Home, "mesh-companions", "catalog-"+q.Catalog.Digest)}
	if e = saveProvision(q.Transfer, &receipt); e != nil {
		return receipt, e
	}
	_, e = publishCompanionCatalog(ctx, q, input, func(identity CatalogIdentity) error {
		receipt.CatalogSeed.Identity = &identity
		return saveProvision(q.Transfer, &receipt)
	}, func() error {
		var proof RuntimeProof
		if err := client.Call(ctx, q.Home, "provision_runtime", nil, &proof); err != nil {
			return err
		}
		if proof.Host != q.Host || proof.UID != q.Transfer.ExpectedUID || proof.SHA256 != q.Transfer.SHA256 || proof.Size != q.Transfer.Size || proof.Version != q.Transfer.Version || proof.Protocol != q.Transfer.Protocol {
			return fmt.Errorf("current started runtime changed; catalog mutation refused")
		}
		return nil
	})
	if e != nil {
		return receipt, e
	}
	receipt.Phase = "seeded_verified"
	if e = verifyProvisionSeed(receipt); e != nil {
		return receipt, e
	}
	if e = saveProvision(q.Transfer, &receipt); e != nil {
		return receipt, e
	}
	return receipt, nil
}
func verifyProvisionSeed(receipt ProvisionReceipt) error {
	if receipt.Phase != "seeded_verified" {
		return nil
	}
	s := receipt.CatalogSeed
	if s == nil || s.Identity == nil || s.Request.Validate() != nil || s.Request.Transfer != receipt.Request || s.Request.Home != receipt.StartHome || s.Request.Host != receipt.StartedHost || s.Destination != filepath.Join(receipt.StartHome, "mesh-companions", "catalog-"+s.Request.Catalog.Digest) {
		return fmt.Errorf("seed receipt differs from exact original install/start/catalog intent")
	}
	if e := ValidateCatalogSeedEvidence(s.Request, s); e != nil {
		return e
	}
	_, e := VerifyCompanionCatalog(receipt.StartHome, s.Request.Catalog, s.Identity)
	return e
}

// ValidateCatalogSeedEvidence checks returned metadata independently of any
// target success label. The source must not settle malformed directory proof.
func ValidateCatalogSeedEvidence(q meshssh.CatalogSeedRequest, s *CatalogSeedReceipt) error {
	if q.Validate() != nil || s == nil || s.Identity == nil || !reflect.DeepEqual(s.Request, q) || s.Destination != filepath.Join(q.Home, "mesh-companions", "catalog-"+q.Catalog.Digest) {
		return fmt.Errorf("catalog receipt differs from exact reviewed request/destination")
	}
	uid, e := strconv.ParseUint(q.Transfer.ExpectedUID, 10, 32)
	if e != nil || s.Identity.Inode == 0 || s.Identity.Owner != uint32(uid) || s.Identity.Mode != uint32(unix.S_IFDIR|0700) {
		return fmt.Errorf("catalog receipt lacks protected owned directory identity")
	}
	return nil
}
