package database

import (
	"context"
	"fmt"
)

// Reconstruct location without advancing a receipt. Only the single durable
// pre-syscall step can explain a lost rename/unlink acknowledgement; unrelated
// disappearance or a replacement must refuse before another action is offered.
func (s *resetStore) validateResetProgress(ctx context.Context, r ResetReceipt) error {
	if err := s.checkForgetMount(r.Plan); err != nil {
		return err
	}
	for _, expected := range r.Plan.Files {
		observe := func(d *Directory) (File, error) {
			f, v, err := d.openSource(expected.Name)
			if f != nil {
				f.Close()
			}
			return v, err
		}
		source, err := observe(s.home)
		if err != nil {
			return err
		}
		quarantine, err := observe(s.operation)
		if err != nil {
			return err
		}
		switch r.Moves[expected.Name] {
		case "absent":
			if source.Present || quarantine.Present {
				return fmt.Errorf("previously absent SQLite pathname appeared")
			}
		case "source":
			if source.Present {
				if quarantine.Present {
					return fmt.Errorf("reviewed database appears at conflicting source/quarantine paths")
				}
				if _, err = exactResetFile(ctx, s.home, expected, true); err != nil {
					return err
				}
			} else {
				if r.Phase != "moving" || r.Step != expected.Name || !quarantine.Present {
					return fmt.Errorf("reviewed source disappeared without exact move intent")
				}
				if _, err = exactResetFile(ctx, s.operation, expected, true); err != nil {
					return err
				}
			}
		case "quarantined":
			if source.Present {
				return fmt.Errorf("source pathname reappeared after quarantine")
			}
			if quarantine.Present {
				if _, err = exactResetFile(ctx, s.operation, expected, true); err != nil {
					return err
				}
			} else if r.Phase != "erasing" || r.Step != expected.Name {
				return fmt.Errorf("quarantined database disappeared without exact erase intent")
			}
		case "erased":
			if source.Present || quarantine.Present {
				return fmt.Errorf("erased database pathname reappeared")
			}
		default:
			return fmt.Errorf("unknown reset database progress")
		}
	}
	if !r.Plan.Forget {
		for _, d := range []*Directory{s.home, s.operation} {
			v, err := d.Observe("state.db-journal", false)
			if err != nil || v.Present {
				return fmt.Errorf("unreviewed rollback journal retained")
			}
		}
	}
	if err := s.validateForgetProgress(ctx, r); err != nil {
		return err
	}
	return ctx.Err()
}
