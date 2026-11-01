package residentservice

// Original funding owners and bindings are never serializable authority. The
// complete explicit operator projection is separate bounded DATA.
func (*WorkerStartupFundingPending) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (*WorkerStartupFundingPending) UnmarshalJSON([]byte) error   { return ErrInvalid }
func (*WorkerStartupFundingView) MarshalJSON() ([]byte, error)    { return nil, ErrInvalid }
func (*WorkerStartupFundingView) UnmarshalJSON([]byte) error      { return ErrInvalid }
func (*WorkerServeFundingBinding) MarshalJSON() ([]byte, error)   { return nil, ErrInvalid }
func (*WorkerServeFundingBinding) UnmarshalJSON([]byte) error     { return ErrInvalid }
