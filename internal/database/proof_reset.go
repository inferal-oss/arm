package database

import (
	"fmt"
	"os"
)

// A persisted one-use marker is current authority provenance, not a repair
// instruction. Its matching completed receipt and consumed pointer must remain
// available; absence/corruption cannot turn into another fresh identity.
func (p *proofReader) readResetMetadata() error {
	d, err := OpenDirectory(p.home)
	if err != nil {
		return err
	}
	defer d.Close()
	seed, _, err := readResetJSON[ResetSeed](d, ResetSeedName)
	if os.IsNotExist(err) {
		if p.resetMetadata != nil {
			return fmt.Errorf("applied reset marker lost its retained authority pointer")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if p.resetMetadata == nil {
		return fmt.Errorf("consumed reset authority lost its applied marker")
	}
	s, err := openResetStore(p.home, seed.Operation, false)
	if err != nil {
		return err
	}
	defer s.close()
	r, _, err := s.readReceipt()
	if err != nil {
		return err
	}
	if err = validSeedReceipt(seed, r); err != nil {
		return err
	}
	if !seed.Installed || seed.Authority != p.result.Host || !sameResetJSON(*p.resetMetadata, resetApplied{ResetSchema, seed.Operation, seed.PlanSHA256, seed.Authority}) {
		return fmt.Errorf("applied reset marker, consumed seed and current authority disagree")
	}
	return nil
}
