package workspace

import (
	"errors"
	"fmt"

	"github.com/inferal-oss/arm/internal/model"
)

// InvalidStateError identifies malformed optional workspace evidence. SQL/driver
// failures are deliberately not this type and retain their ordinary semantics.
type InvalidStateError struct{ cause error }

func (e *InvalidStateError) Error() string      { return "invalid workspace evidence: " + e.cause.Error() }
func (e *InvalidStateError) Unwrap() error      { return e.cause }
func invalidState(e error) error                { return &InvalidStateError{e} }
func invalidf(format string, args ...any) error { return invalidState(fmt.Errorf(format, args...)) }
func stateProblem(e error) error {
	var invalid *InvalidStateError
	if errors.As(e, &invalid) {
		return problem("workspace_state_invalid", invalid.Error())
	}
	return e
}
func IsInvalidState(e error) bool {
	var invalid *InvalidStateError
	var api *model.Error
	return errors.As(e, &invalid) || errors.As(e, &api) && api.Code == "workspace_state_invalid"
}
