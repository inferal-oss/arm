package cargoprofile

import (
	"fmt"
	"reflect"
)

// ExplicitTargets walks the validated flag grammar, so flag-looking package or
// feature values cannot be mistaken for actual target selectors in evidence.
func (c Command) ExplicitTargets() ([]Target, error) {
	canonical, e := Parse(c.Original, float64(c.Jobs))
	if e != nil || !reflect.DeepEqual(canonical, c) {
		return nil, fmt.Errorf("canonical parsed Cargo command required for target evidence")
	}
	out := []Target{}
	for i := 1; i < len(c.Arguments); i++ {
		flag := c.Arguments[i]
		if flag == "--target" {
			i++
			out = append(out, Target{Selector: c.Arguments[i], Origin: "explicit-command"})
		} else if _, ok := valueFlags[flag]; ok {
			i++
		}
	}
	return out, nil
}
