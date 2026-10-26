package cli

import (
	"flag"
	"fmt"
	"time"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshsshrelay"
	"github.com/yrashk/arm/internal/model"
)

// mesh SSH routing policy is native-only and independently reviewed. It never
// modifies direct peer grants or message-workspace admission.
func (a *app) meshSSHRelay(args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("usage: arm mesh ssh-relay show|grant-relay|revoke-relay|grant-destination|revoke-destination")
	}
	action, args := args[0], args[1:]
	var local mesh.Identity
	if e := a.call("mesh_identity", struct{}{}, &local); e != nil {
		return 1, e
	}
	if action == "show" {
		if len(args) != 0 {
			return 2, fmt.Errorf("show accepts no arguments")
		}
		p, e := meshsshrelay.ReadPolicy(a.home, local.Host, local.Fingerprint)
		if e != nil {
			return 1, e
		}
		return 0, a.print(p)
	}
	f := a.flags("mesh ssh-relay " + action)
	workspace := f.String("workspace", "", "exact admitted workspace ID")
	generation := f.String("generation", "", "exact workspace generation (required for relay opt-in)")
	source := f.String("source", "", "original source stable host ID (destination grant)")
	ttl := f.Duration("ttl", time.Hour, "maximum destination SSH execution lease")
	expires := f.Duration("expires", 24*time.Hour, "grant validity from current review")
	cpu := f.Float64("cpu", 1, "destination logical CPU ceiling")
	memory := f.String("memory", "0", "destination memory ceiling")
	disk := f.String("disk", "0", "destination disk ceiling")
	relayCPU := f.Float64("relay-cpu", .05, "local CPU reservation for each active transit stream")
	relayMemory := f.String("relay-memory", "8MiB", "local memory reservation for each active transit stream")
	relayRate := f.String("relay-rate", "64MiB", "shared byte-per-second limit for both transit directions")
	ssh := f.Bool("ssh", true, "permit ordinary routed SSH (legacy relay or destination grant)")
	syncClass := f.Bool("sync", false, "permit routed sync when supported (legacy relay or destination grant)")
	reason := f.String("reason", "", "required operator review reason")
	yes := f.Bool("yes", false, "apply exact reviewed grant change")
	dry := f.Bool("dry-run", false, "show exact grant change without mutation")
	if e := a.parse(f, args, false); e != nil {
		return 2, e
	}
	if action == "grant-relay" && meshsshrelay.ForwardReviewEnabled {
		classFlag := false
		f.Visit(func(v *flag.Flag) {
			if v.Name == "ssh" || v.Name == "sync" {
				classFlag = true
			}
		})
		if classFlag {
			return 2, fmt.Errorf("relay grants carry opaque workspace transport; --ssh and --sync select only destination grants")
		}
	}
	if f.NArg() != 0 || *workspace == "" || *reason == "" || *expires <= 0 || *expires > 365*24*time.Hour {
		return 2, fmt.Errorf("exact workspace, bounded expiry and reason required")
	}
	if action != "grant-relay" && action != "revoke-relay" && action != "grant-destination" && action != "revoke-destination" {
		return 2, fmt.Errorf("unsupported mesh SSH relay action")
	}
	var context meshsshrelay.Context
	if action == "grant-destination" {
		if *source == "" {
			return 2, fmt.Errorf("original --source host required")
		}
		if e := a.call("mesh_ssh_route_context", struct {
			Workspace string `json:"workspace"`
			Source    string `json:"source"`
			Target    string `json:"target"`
		}{*workspace, *source, local.Host}, &context); e != nil {
			return 1, e
		}
		if context.Access.Local.Host != local.Host || context.Access.Destination.Host != local.Host || context.Account == "" || context.PublicKey == "" {
			return 1, model.Err("forbidden", "current local destination workspace and SSH service required")
		}
		if *generation != "" && *generation != context.Access.Generation {
			return 1, model.Err("workspace_generation_changed", "requested destination grant generation differs from current pinned workspace")
		}
	}
	mem, e := sshBytes(*memory)
	if e != nil {
		return 2, e
	}
	ds, e := sshBytes(*disk)
	if e != nil {
		return 2, e
	}
	transitMemory, e := sshBytes(*relayMemory)
	if e != nil {
		return 2, e
	}
	bytesPerSecond, e := sshBytes(*relayRate)
	if e != nil {
		return 2, e
	}
	change := func(p *meshsshrelay.Policy) error {
		switch action {
		case "grant-relay":
			if *generation == "" {
				return fmt.Errorf("exact --generation required for relay opt-in")
			}
			g := meshsshrelay.RelayGrant{Workspace: *workspace, Generation: *generation, Local: local.Host, Revision: 1,
				Transport: meshsshrelay.ForwardReviewEnabled, SSH: !meshsshrelay.ForwardReviewEnabled && *ssh, Sync: !meshsshrelay.ForwardReviewEnabled && *syncClass, Transit: model.Resources{CPU: *relayCPU, Memory: transitMemory},
				BytesPerSecond: bytesPerSecond, Expires: time.Now().UTC().Add(*expires)}
			for i, old := range p.Relays {
				if old.Workspace == g.Workspace && old.Generation == g.Generation {
					g.Revision = old.Revision + 1
					p.Relays[i] = g
					return nil
				}
			}
			p.Relays = append(p.Relays, g)
		case "revoke-relay":
			for i, old := range p.Relays {
				if old.Workspace == *workspace && old.Generation == *generation && old.Local == local.Host {
					p.Relays = append(p.Relays[:i], p.Relays[i+1:]...)
					return nil
				}
			}
			return fmt.Errorf("exact relay opt-in absent")
		case "grant-destination":
			v := context.Access
			g := meshsshrelay.DestinationGrant{Workspace: v.Workspace, Generation: v.Generation, Source: v.Origin.Host, Target: v.Destination.Host,
				SourceGeneration: v.OriginGeneration, TargetGeneration: v.DestinationGeneration,
				DirectoryPin: v.Directory.Fingerprint, DirectorySPKI: v.Directory.SPKI,
				SourcePin: v.Origin.Fingerprint, SourceSPKI: v.Origin.SPKI, TargetPin: v.Destination.Fingerprint, TargetSPKI: v.Destination.SPKI,
				SSHHostKeyPin: context.HostKeySHA256, Account: context.Account, Owner: context.Owner, Revision: 1, SSH: *ssh, Sync: *syncClass,
				MaxTTL: *ttl, Limits: model.Resources{CPU: *cpu, Memory: mem, Disk: ds}, Expires: time.Now().UTC().Add(*expires)}
			for i, old := range p.Destinations {
				if old.Workspace == g.Workspace && old.Generation == g.Generation && old.Source == g.Source {
					g.Revision = old.Revision + 1
					p.Destinations[i] = g
					return nil
				}
			}
			p.Destinations = append(p.Destinations, g)
		case "revoke-destination":
			if *source == "" || *generation == "" {
				return fmt.Errorf("exact --source and --generation required for revoke")
			}
			for i, old := range p.Destinations {
				if old.Workspace == *workspace && old.Generation == *generation && old.Source == *source && old.Target == local.Host {
					p.Destinations = append(p.Destinations[:i], p.Destinations[i+1:]...)
					return nil
				}
			}
			return fmt.Errorf("exact destination grant absent")
		}
		return nil
	}
	auditAction := "grant"
	if action == "revoke-relay" || action == "revoke-destination" {
		auditAction = "revoke"
	}
	review, e := meshsshrelay.ReviewPolicy(a.home, local.Host, local.Fingerprint, auditAction, *reason, change)
	if e != nil {
		return 1, e
	}
	plan := map[string]any{"operation": action, "before_sha256": review.BeforeSHA256, "after_sha256": review.AfterSHA256,
		"before": review.Before, "after": review.After, "effect": "Generic bounded relay transport; only final destinations grant SSH execution or named TCP services. Current membership and final destination authority are rechecked."}
	if e = a.meshConfirm(plan, *yes, *dry); e != nil {
		return 1, e
	}
	if *dry {
		return 0, nil
	}
	if e = meshsshrelay.CommitPolicy(review); e != nil {
		return 1, e
	}
	return 0, a.print(review.After)
}
