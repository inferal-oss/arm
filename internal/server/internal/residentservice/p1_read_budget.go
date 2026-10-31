package residentservice

// Parser/admission DATA only, not a grant or successful read. Production derives
// mode and kind from its actual retained original owner/operation and records the
// stage on its original read call BEFORE I/O. The exact five-stage P1 cut stops
// at BeforeStart. Post-Start/ACK/current-authority read stages remain unsupported
// and must be explicitly designed/funded/reviewed before changing this list.
func journalReadBudgetData(p1 bool, prior []string, next string) error {
	if !p1 {
		if len(prior) >= 4 {
			return ErrInvalid
		}
		return nil
	}
	stages := [...]string{"open", "debit", "p1-funding", "p1-binding", "p1-before-start"}
	if len(prior) >= len(stages) || next != stages[len(prior)] {
		return ErrInvalid
	}
	for i, stage := range prior {
		if stage != stages[i] {
			return ErrInvalid
		}
	}
	return nil
}
