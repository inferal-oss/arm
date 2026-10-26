package database

import (
	"fmt"
	"os"
)

type forgetMountProbe func(*os.File) (string, error)

func requireForgetMount(f *os.File, want string, probe forgetMountProbe) error {
	got, err := probe(f)
	if err != nil {
		return err
	}
	if want == "" || got != want {
		return fmt.Errorf("forget refuses a mount transition or changed mount identity")
	}
	return nil
}
func (s *resetStore) checkForgetMount(plan ResetPlan) error {
	if !plan.Forget {
		return nil
	}
	s.forgetMount = plan.ForgetMount
	for _, d := range []*Directory{s.home, s.container, s.operation} {
		if err := requireForgetMount(d.FD, plan.ForgetMount, forgetMountIdentity); err != nil {
			return err
		}
	}
	return nil
}
func forgetFileGuard(d *Directory, expected File, mount string) (*os.File, error) {
	if err := requireForgetMount(d.FD, mount, forgetMountIdentity); err != nil {
		return nil, err
	}
	f, v, err := d.openSource(expected.Name)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, nil
	}
	if !sameIdentity(v, expected) {
		f.Close()
		return nil, fmt.Errorf("forget target identity changed")
	}
	if err = requireForgetMount(f, mount, forgetMountIdentity); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
