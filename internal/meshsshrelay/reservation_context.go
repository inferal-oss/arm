package meshsshrelay

import (
	"context"
	"errors"
)

type reservationKey struct{}
type reservationChecks []func(context.Context) error

// withReservation retains every original local hold through downstream route
// callbacks. A fresh explicit check is required at each admission/start cut.
func withReservation(ctx context.Context, check func(context.Context) error) (context.Context, error) {
	if ctx == nil || check == nil {
		return nil, errors.New("original resource hold check required")
	}
	previous, _ := ctx.Value(reservationKey{}).(reservationChecks)
	if len(previous) >= 8 {
		return nil, errors.New("resource hold chain exceeds route bound")
	}
	checks := append(append(reservationChecks(nil), previous...), check)
	guarded := context.WithValue(ctx, reservationKey{}, checks)
	return guarded, CheckReservation(guarded)
}

// CheckReservation checks all original resource holds carried by this route.
// Pre-reservation authorization contexts have no holds. Carrier admission
// separately requires a nonnil check from each successful reservation.
func CheckReservation(ctx context.Context) error {
	if ctx == nil {
		return errors.New("live route context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	checks, _ := ctx.Value(reservationKey{}).(reservationChecks)
	for _, check := range checks {
		if err := check(ctx); err != nil {
			return err
		}
	}
	return ctx.Err()
}
