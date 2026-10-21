package ports

// Namespace is host-local; ARM's durable host ID supplies the outer identity.
// macOS does not expose Linux-style per-process network namespaces.
func Namespace() (string, error) { return "host", nil }
