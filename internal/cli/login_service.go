package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"getarm.org/internal/loginservice"
)

func (a *app) offerLoginService() error {
	if a.machine() || !a.inputTTY {
		return nil
	}
	if err := a.ctx.Err(); err != nil {
		return err
	}
	if err := a.promptLoginService(); err != nil {
		if interrupted := a.ctx.Err(); interrupted != nil {
			return interrupted
		}
		// This is optional setup. The backend was already confirmed ready.
		fmt.Fprintln(a.err, "Login service setup: "+terminalSafe(err.Error()))
	}
	return nil
}

func (a *app) promptLoginService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	service, err := loginservice.ForUser(exe, a.home)
	if err != nil || service == nil {
		return err
	}
	if enabled, err := service.Enabled(); enabled || err != nil {
		return err
	}
	fmt.Fprintf(a.out, "\nStart ARM automatically at login?\n  %s service %s\n  Definition: %s\n", service.Manager, service.Name, terminalSafe(service.Path))
	if service.EnableLink != "" {
		fmt.Fprintln(a.out, "  Enable link: "+terminalSafe(service.EnableLink))
	}
	fmt.Fprintf(a.out, "  Executable: %s\n  ARM home: %s\n", terminalSafe(service.Executable), terminalSafe(service.Home))
	fmt.Fprintln(a.out, "The service will use this ARM home's config.toml and defaults at the next login.")
	if _, err := fmt.Fprint(a.out, a.paint("1;33", "Enable start at login? [y/N] ")); err != nil {
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
	if err := service.Enable(); err != nil {
		return err
	}
	_, err = fmt.Fprintln(a.out, "Login service installed for the next login.")
	return err
}
