package manager

// Registered profiles bind documentation identifiers to actual admission
// validators. Adding a Markdown card alone cannot manufacture runtime support.
type profileValidator func([]string, float64) error

var profileValidators = map[string]profileValidator{
	"rust-build":     validateCargo,
	"gradle-service": validateGradleService,
	"gradle-build":   validateGradleBuild,
}

// SupportsProfile is shared by admission and the embedded knowledge build gate.
func SupportsProfile(id string) bool {
	validator, ok := profileValidators[id]
	return ok && validator != nil
}
