package residentrefs

// NewFixedWorkerChildPending is the only worker-scoped fixed13 producer. No
// caller-selected role, descriptor, index or scope is accepted. This is pure
// allocation; actual sealing/delegation/materialization remains mandatory.
func NewFixedWorkerChildPending(destination **ChildPending) error {
	return newFixedChildPending(destination, workerChildCore)
}
