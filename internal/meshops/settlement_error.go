package meshops

import "github.com/inferal-oss/arm/internal/model"

// settlementFailure retains the native cause for local diagnostics while keeping
// arbitrary filesystem, decoder and marshaler error text out of public output.
// Standard traversal exposes only the public error: a native control sentinel
// must not turn an incomplete outcome into help, cancellation or an argument
// error. Local diagnostics can explicitly inspect SettlementCause instead.
type settlementFailure struct {
	stage  string
	cause  error
	public *model.Error
}

func newSettlementFailure(stage string, cause error) error {
	return &settlementFailure{
		stage:  stage,
		cause:  cause,
		public: &model.Error{Code: "mesh_operation_incomplete", Message: "operation outcome could not be durably settled at " + stage + "; inspect its exact key before recovery"},
	}
}

func (e *settlementFailure) Error() string { return e.public.Error() }
func (e *settlementFailure) Unwrap() error { return e.public }

// SettlementCause is opt-in diagnostic evidence, not a public outcome.
func (e *settlementFailure) SettlementCause() error { return e.cause }
