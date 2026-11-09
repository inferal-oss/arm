package cli

import (
	"bufio"
	"fmt"
	"strings"

	"getarm.org/internal/loginservice"
)

func (a *app) offerLoginService(executable func() (string, error)) error {
	if a.machine() || !a.inputTTY {
		return nil
	}
	if err := a.ctx.Err(); err != nil {
		return err
	}
	if err := a.promptLoginService(executable); err != nil {
		if interrupted := a.ctx.Err(); interrupted != nil {
			return interrupted
		}
		// This is optional setup. The backend was already confirmed ready.
		fmt.Fprintln(a.err, "Login service setup: "+terminalSafe(err.Error()))
	}
	return nil
}

func (a *app) promptLoginService(executable func() (string, error)) error {
	exe, err := executable()
	if err != nil {
		return err
	}
	service, err := loginservice.ForUser(a.ctx, exe, a.home)
	if err != nil || service == nil {
		return err
	}
	setup, err := service.Review()
	if err != nil || setup == nil {
		return err
	}
	question := "Enable start at login? [y/N] "
	if setup.PolicyOverride != "" {
		question = "Update the login service's shutdown policy? [y/N] "
	}
	fmt.Fprintf(a.out, "\nARM login service\n  %s service %s\n  Definition: %s\n", service.Manager, service.Name, terminalSafe(service.Path))
	if service.EnableLink != "" {
		fmt.Fprintln(a.out, "  Enable link: "+terminalSafe(service.EnableLink))
	}
	if setup.PolicyOverride != "" {
		fmt.Fprintln(a.out, "  Shutdown policy drop-in: "+terminalSafe(setup.PolicyOverride))
		fmt.Fprintln(a.out, "  This confines systemd's stop signals to the ARM daemon.")
	}
	fmt.Fprintf(a.out, "  Executable: %s\n  ARM home: %s\n", terminalSafe(service.Executable), terminalSafe(service.Home))
	fmt.Fprintln(a.out, "The service will use this ARM home's config.toml and defaults at the next login.")
	fmt.Fprintln(a.out, "The service and its workloads inherit the service manager's environment. Configure PATH or JAVA_HOME in the service if your tools need shell settings.")
	if _, err := fmt.Fprint(a.out, a.paint("1;33", question)); err != nil {
		return err
	}
	answer := make(chan string, 1)
	go func() {
		line, err := bufio.NewReaderSize(a.in, 256).ReadSlice('\n')
		if err != nil {
			answer <- ""
			return
		}
		answer <- strings.ToLower(strings.TrimSpace(string(line)))
	}()
	select {
	case <-a.ctx.Done():
		fmt.Fprintln(a.out)
		return a.ctx.Err()
	case reply := <-answer:
		fmt.Fprintln(a.out)
		if reply != "y" && reply != "yes" {
			return nil
		}
	}
	if err := a.ctx.Err(); err != nil {
		return err
	}
	if err := setup.Apply(a.ctx); err != nil {
		return err
	}
	if setup.PolicyOverride != "" {
		_, err = fmt.Fprintln(a.out, "Login service updated. Run systemctl --user daemon-reload to apply the shutdown policy in this session.")
	} else {
		_, err = fmt.Fprintln(a.out, "Login service installed for the next login.")
	}
	return err
}
