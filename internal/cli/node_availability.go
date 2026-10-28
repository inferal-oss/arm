package cli

import (
	"flag"
	"fmt"
	"getarm.org/internal/model"
	"getarm.org/internal/nodeavailability"
)

// Prepared command entry for `arm node availability`; the shared command router
// must opt in after the read/set backend operations and proof reader are wired.
func (a *app) nodeAvailability(args []string) (int, error) {
	setting := len(args) > 0 && args[0] == "set"
	if setting {
		args = args[1:]
	}
	command := "node availability"
	if setting {
		command += " set"
	}
	f := a.flags(command)
	host := f.String("host", "localhost", "exact local authority or enrolled peer for read-only inspection")
	f.Bool("json", false, "JSON output")
	var profile, reason, key *string
	var revision *uint64
	if setting {
		profile = f.String("profile", "", "owner declaration: unspecified, stable or intermittent")
		reason = f.String("reason", "", "private local reason (1–1024 bytes)")
		key = f.String("key", "", "original update key; retain it if the result is uncertain")
		revision = f.Uint64("expected-revision", 0, "exact current revision; read availability first")
	}
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 0 {
		return 2, model.Err("invalid_request", "node availability accepts no operands; choose the host with --host")
	}
	if !setting {
		if err := a.resolveValidatedAliases(f); err != nil {
			return 1, err
		}
		var report nodeavailability.Report
		if err := a.call("node_availability_read_v1", struct {
			Host string `json:"host"`
		}{*host}, &report); err != nil {
			return 1, err
		}
		return 0, a.print(report)
	}
	// This setting describes SELF. A remote selector must never mutate another
	// host's declaration or fall back to changing localhost.
	if *host != "localhost" {
		return 2, model.Err("invalid_request", "availability profile changes are local-owner-only; omit --host")
	}
	seenRevision := false
	f.Visit(func(value *flag.Flag) {
		if value.Name == "expected-revision" {
			seenRevision = true
		}
	})
	if !seenRevision {
		return 2, model.Err("invalid_request", "read availability and supply --expected-revision explicitly")
	}
	update := nodeavailability.Update{Key: *key, ExpectedRevision: *revision, Profile: *profile, Reason: *reason}
	if err := update.Validate(); err != nil {
		return 2, model.Err("invalid_request", err.Error())
	}
	var result nodeavailability.Document
	if err := a.call("node_availability_set_v1", update, &result); err != nil {
		if api, ok := err.(*model.Error); ok && (api.Code == "revision_conflict" || api.Code == "idempotency_conflict" || api.Code == "invalid_request" || api.Code == "backend_stopping" || api.Code == "availability_metadata_invalid") {
			return 1, err
		}
		return 1, model.Err("availability_update_unknown", fmt.Sprintf("owner profile may have changed; original key %q, expected revision %d. Read current availability before another explicit change", update.Key, update.ExpectedRevision))
	}
	return 0, a.print(result.Report(result.Host))
}
