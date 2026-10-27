package cli

import (
	"context"
	"fmt"
	"getarm.org/internal/client"
	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshops"
	"getarm.org/internal/meshssh"
	"getarm.org/internal/model"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type meshConnectRequest = meshops.ConnectRequest

func (a *app) meshLifecycle(action string, args []string) (int, error) {
	if action == "disconnect" {
		return a.meshDisconnect(args)
	}
	f := a.flags("mesh connect")
	remoteHome := f.String("remote-home", "", "target ARM state directory")
	binary := f.String("binary", "arm", "remote ARM executable to inspect; absent nodes use a separately reviewed local companion")
	artifact := f.String("artifact", "", "trusted local target-OS/architecture ARM companion (no network download)")
	installRoot := f.String("install-root", "", "exact private target artifact root (default user-level content-addressed install)")
	grant := f.String("grant", "observe-host", "permissions this node receives on target")
	reverse := f.String("reverse-grant", "", "target permissions on this node; none by default")
	contacts := f.String("contacts", "", "exact contact IDs allowed by forward/reverse contact grants")
	inbox := f.Bool("operator-inbox", true, "allow operator inbox when message explicitly granted")
	cpu := f.Float64("cpu", 2, "per-peer aggregate CPU ceiling")
	memory := f.String("memory", "1GiB", "per-peer memory ceiling")
	disk := f.String("disk", "1GiB", "per-peer disk ceiling")
	ttl := f.Duration("max-ttl", time.Hour, "maximum granted remote lease lifetime")
	reason := f.String("reason", "operator connected direct ARM peers", "audited connection purpose")
	key := f.String("key", "", "exact lifecycle retry key (generated)")
	localIP := f.String("local-ip", "", "explicit local assigned interface IP; default direct SSH route")
	remoteIP := f.String("remote-ip", "", "explicit target assigned interface IP; default direct SSH route")
	public := f.Bool("allow-public-listen", false, "explicitly permit reviewed nonprivate interface exposure")
	replace := f.Bool("replace-revoked", false, "explicitly re-pair previously revoked records after review")
	yes := f.Bool("yes", false, "apply reviewed setup and directional grants")
	dry := f.Bool("dry-run", false, "inspect only; create no state, daemon, key or socket")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if (*remoteHome != "" && !filepath.IsAbs(*remoteHome)) || f.NArg() != 1 || strings.HasPrefix(f.Arg(0), "-") || !regexp.MustCompile(`^(arm|/[A-Za-z0-9_./-]+)$`).MatchString(*binary) || strings.TrimSpace(*reason) == "" || len(*reason) > 4096 {
		return 2, fmt.Errorf("connect requires SSH_DEST and a safe installed --binary; reason must be1..4096 bytes")
	}
	mem, err := bytesValue(*memory)
	if err != nil {
		return 2, err
	}
	d, err := bytesValue(*disk)
	if err != nil {
		return 2, err
	}
	makeGrant := func(text string) mesh.Grant {
		p := []string{}
		if text != "" {
			p = strings.Split(text, ",")
		}
		ids := []string{}
		if *contacts != "" {
			ids = strings.Split(*contacts, ",")
		}
		return mesh.Grant{Permissions: p, Contacts: ids, OperatorInbox: *inbox, Limits: model.Resources{CPU: *cpu, Memory: mem, Disk: d}, MaxTTL: *ttl}
	}
	request := meshConnectRequest{Destination: f.Arg(0), RemoteHome: *remoteHome, Binary: *binary, LocalIP: *localIP, RemoteIP: *remoteIP, Reason: *reason, Forward: makeGrant(*grant), Reverse: makeGrant(*reverse), Public: *public, ReplaceRevoked: *replace}
	if err = request.Forward.Validate(); err != nil {
		return 2, err
	}
	if err = request.Reverse.Validate(); err != nil {
		return 2, err
	}
	if *key == "" {
		*key = local.ID() + local.ID()
	}
	if !mesh.ValidKey(*key) {
		return 2, fmt.Errorf("connect requires a nonempty key up to128 bytes")
	}
	handled, e := a.provisionMissingMesh(*key, &request, *artifact, *installRoot, *yes, *dry)
	if e != nil {
		return 1, e
	}
	if handled {
		return 0, nil
	}
	return a.connectMesh(*key, request, *yes, *dry)
}
func (a *app) meshCoordinator() *meshops.Coordinator {
	return &meshops.Coordinator{Context: a.ctx, Home: a.home, Machine: a.machine(), Inspect: a.inspectMeshLifecycle, Ensure: a.ensureMeshBackend, Call: func(ctx context.Context, op string, q, result any) error {
		return client.Call(ctx, a.home, op, q, result)
	}, SSH: meshssh.Call, Print: a.print, Confirm: a.meshConfirm}
}
func (a *app) connectMesh(key string, request meshConnectRequest, yes, dry bool) (int, error) {
	return a.meshCoordinator().Connect(key, request, yes, dry)
}
func (a *app) meshDisconnect(args []string) (int, error) {
	f := a.flags("mesh disconnect")
	reason := f.String("reason", "operator disconnected direct ARM peers", "audited revocation reason")
	key := f.String("key", "", "exact disconnect retry key (generated)")
	yes := f.Bool("yes", false, "apply reviewed revocation")
	dry := f.Bool("dry-run", false, "inspect without changes")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 1 || strings.TrimSpace(*reason) == "" || len(*reason) > 4096 {
		return 2, fmt.Errorf("disconnect requires HOST and a bounded reason")
	}
	if *key == "" {
		*key = local.ID() + local.ID()
	}
	if !mesh.ValidKey(*key) {
		return 2, fmt.Errorf("invalid disconnect key")
	}
	if err := a.resolveValidatedAliases(f); err != nil {
		return 1, err
	}
	return a.meshCoordinator().Disconnect(*key, f.Arg(0), *reason, *yes, *dry)
}
