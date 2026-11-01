package mesh

// Only private fixed-role constructors choose a pin scope. The public legacy
// factory keeps its old signature and scope; worker wrappers expose no inner
// pin, descriptor, old runtime borrower or original parent closer getter.
type residentImagePinCoreScope uint8

const (
	legacyResidentImagePinCore residentImagePinCoreScope = iota
	workerResidentImagePinCore
)
