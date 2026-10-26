package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/yrashk/arm/internal/kubeprovision"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
)

// meshKubernetes is intentionally reached only by a small mesh command
// dispatch addition. It creates no trust or resource grant. The review/apply
// split and exact-key journal remain inside kubeprovision.
func (a *app) meshKubernetes(args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("mesh kubernetes requires contexts, nodes, keygen, plan, apply, lookup or ship")
	}
	action, rest := args[0], args[1:]
	switch action {
	case "contexts":
		f := a.flags("mesh kubernetes contexts")
		if err := a.parse(f, rest, false); err != nil {
			return 2, err
		}
		if f.NArg() != 0 {
			return 2, fmt.Errorf("contexts accepts no arguments")
		}
		choices, err := kubeprovision.Contexts()
		if err != nil {
			return 1, err
		}
		return 0, a.print(choices)
	case "keygen":
		f := a.flags("mesh kubernetes keygen")
		if err := a.parse(f, rest, false); err != nil {
			return 2, err
		}
		if f.NArg() != 0 {
			return 2, fmt.Errorf("keygen accepts no arguments")
		}
		return 0, a.print(map[string]string{"key": local.ID() + local.ID()})
	case "nodes":
		return a.meshKubernetesNodes(rest)
	case "plan", "apply":
		return a.meshKubernetesPlan(action, rest)
	case "lookup":
		return a.meshKubernetesLookup(rest)
	case "ship":
		return a.meshKubernetesShip(rest)
	default:
		return 2, fmt.Errorf("unknown mesh kubernetes action %q", action)
	}
}

func kubeSelectedContext(name string) (kubeprovision.ContextChoice, error) {
	choices, err := kubeprovision.Contexts()
	if err != nil {
		return kubeprovision.ContextChoice{}, err
	}
	for _, c := range choices {
		if name != "" && c.Name == name {
			return c, nil
		}
	}
	if name != "" {
		return kubeprovision.ContextChoice{}, fmt.Errorf("Kubernetes context %q not found", name)
	}
	for _, c := range choices {
		if c.Current {
			return c, nil
		}
	}
	if len(choices) == 1 {
		return choices[0], nil
	}
	return kubeprovision.ContextChoice{}, fmt.Errorf("select one Kubernetes context from mesh kubernetes contexts")
}

func (a *app) meshKubernetesNodes(args []string) (int, error) {
	f := a.flags("mesh kubernetes nodes")
	contextName := f.String("context", "", "selected kubeconfig context; defaults to current")
	namespaceName := f.String("namespace", "", "namespace; defaults to selected context")
	query := f.String("query", "", "typeahead search for Node name, architecture, zone or instance type")
	limit := f.Int("limit", 50, "maximum matching Nodes, 1..100")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 0 || *limit < 1 || *limit > 100 {
		return 2, fmt.Errorf("nodes accepts no arguments and limit must be 1..100")
	}
	choice, err := kubeSelectedContext(*contextName)
	if err != nil {
		return 2, err
	}
	if *namespaceName == "" {
		*namespaceName = choice.Namespace
	}
	api, err := kubeprovision.OpenContext(choice.Name)
	if err != nil {
		return 1, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	cluster, err := api.Cluster(ctx)
	if err != nil {
		return 1, err
	}
	namespace, err := api.Namespace(ctx, *namespaceName)
	if err != nil {
		return 1, err
	}
	provider := kubeprovision.Provider{API: api}
	nodes, err := provider.Candidates(ctx, *query, *limit)
	if err != nil {
		return 1, err
	}
	return 0, a.print(struct {
		Cluster   kubeprovision.Cluster   `json:"cluster"`
		Namespace kubeprovision.Namespace `json:"namespace"`
		Nodes     []kubeprovision.Node    `json:"nodes"`
	}{cluster, namespace, nodes})
}

func (a *app) kubeJournal() kubeprovision.FileJournal {
	return kubeprovision.FileJournal{Root: filepath.Join(a.home, "kube-provision")}
}

func (a *app) meshKubernetesPlan(action string, args []string) (int, error) {
	f := a.flags("mesh kubernetes " + action)
	contextName := f.String("context", "", "selected kubeconfig context; defaults to current")
	namespaceName := f.String("namespace", "", "namespace; defaults to selected context")
	node := f.String("node", "", "Node name selected from nodes inventory")
	nodeUID := f.String("node-uid", "", "exact selected Node UID from nodes inventory")
	image := f.String("image", "", "reviewed immutable multiarch image@sha256:digest")
	binary := f.String("binary", "", "trusted local target-Linux ARM executable")
	catalog := f.String("catalog-sha256", "", "reviewed companion catalog manifest SHA-256")
	key := f.String("key", "", "exact 64-hex operation key from keygen")
	reason := f.String("reason", "", "audited Pod installation purpose")
	cpuRequest := f.String("cpu-request", "250m", "Pod CPU scheduler request")
	cpuLimit := f.String("cpu-limit", "500m", "Pod CPU limit")
	memoryRequest := f.String("memory-request", "256Mi", "Pod memory scheduler request")
	memoryLimit := f.String("memory-limit", "512Mi", "Pod memory limit")
	ephemeralRequest := f.String("ephemeral-request", "512Mi", "Pod ephemeral-storage request")
	ephemeralLimit := f.String("ephemeral-limit", "1Gi", "Pod ephemeral-storage limit and private volume cap")
	expect := f.String("expect-plan", "", "exact reviewed plan SHA-256 required for apply")
	yes := f.Bool("yes", false, "apply the exact reviewed Pod plan")
	dry := f.Bool("dry-run", false, "show plan without mutation")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 0 || *node == "" || *nodeUID == "" || *image == "" || *binary == "" || *catalog == "" || *key == "" || *reason == "" {
		return 2, fmt.Errorf("plan needs --node, --node-uid, --image, --binary, --catalog-sha256, --key and --reason")
	}
	choice, err := kubeSelectedContext(*contextName)
	if err != nil {
		return 2, err
	}
	if *namespaceName == "" {
		*namespaceName = choice.Namespace
	}
	artifact, err := mesh.InspectArtifact(*binary, *reason)
	if err != nil {
		return 1, err
	}
	api, err := kubeprovision.OpenContext(choice.Name)
	if err != nil {
		return 1, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	provider := kubeprovision.Provider{API: api, Journal: a.kubeJournal()}
	plan, err := provider.Prepare(ctx, kubeprovision.Draft{Key: *key, Owner: local.CurrentIdentity().UID, Reason: *reason, Context: choice.Name, Namespace: *namespaceName, SelectedNode: *node, SelectedNodeUID: *nodeUID, Image: *image, BinarySHA256: artifact.SHA256, BinaryBytes: artifact.Size, CatalogSHA256: *catalog, Resources: kubeprovision.Resources{CPURequest: *cpuRequest, CPULimit: *cpuLimit, MemoryRequest: *memoryRequest, MemoryLimit: *memoryLimit, EphemeralRequest: *ephemeralRequest, EphemeralLimit: *ephemeralLimit}})
	if err != nil {
		return 1, err
	}
	if artifact.OS != "linux" || artifact.Arch != plan.Review.Node.Arch {
		return 1, model.Err("artifact_platform_changed", "selected Node platform differs from exact trusted ARM executable")
	}
	if action == "plan" || *dry {
		return 0, a.print(plan)
	}
	if *expect == "" || *expect != plan.SHA256 {
		return 2, model.Err("review_changed", "apply requires --expect-plan matching a current dry-run plan")
	}
	if err := a.meshConfirm(plan, *yes, false); err != nil {
		return 1, err
	}
	if err := local.Prepare(a.home); err != nil {
		return 1, err
	}
	result, err := provider.Apply(ctx, plan)
	if printErr := a.print(result); printErr != nil {
		return 1, printErr
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

func (a *app) kubeOriginal(ctx context.Context, key string) (kubeprovision.Provider, kubeprovision.Record, error) {
	journal := a.kubeJournal()
	record, ok, err := journal.Read(ctx, key)
	if err != nil {
		return kubeprovision.Provider{}, record, err
	}
	if !ok {
		return kubeprovision.Provider{}, record, model.Err("not_found", "Kubernetes provisioning key not found")
	}
	api, err := kubeprovision.OpenReviewedContext(record.Plan)
	if err != nil {
		return kubeprovision.Provider{}, record, err
	}
	return kubeprovision.Provider{API: api, Journal: journal}, record, nil
}

func (a *app) meshKubernetesLookup(args []string) (int, error) {
	f := a.flags("mesh kubernetes lookup")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 1 {
		return 2, fmt.Errorf("lookup requires the original exact key")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	p, record, err := a.kubeOriginal(ctx, f.Arg(0))
	if err != nil {
		return 1, err
	}
	observed, err := p.Lookup(ctx, f.Arg(0))
	if err == nil {
		record = observed
	}
	if printErr := a.print(record); printErr != nil {
		return 1, printErr
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

func (a *app) meshKubernetesShip(args []string) (int, error) {
	f := a.flags("mesh kubernetes ship")
	binary := f.String("binary", "", "exact trusted binary matching original plan")
	expect := f.String("expect-plan", "", "original reviewed plan SHA-256")
	yes := f.Bool("yes", false, "ship exact binary to owned Pod")
	dry := f.Bool("dry-run", false, "review without shipment")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 1 || *binary == "" || *expect == "" {
		return 2, fmt.Errorf("ship requires KEY --binary FILE --expect-plan SHA256")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	defer cancel()
	p, record, err := a.kubeOriginal(ctx, f.Arg(0))
	if err != nil {
		return 1, err
	}
	if *expect != record.Plan.SHA256 {
		return 2, model.Err("review_changed", "binary shipment plan digest changed")
	}
	artifact, err := mesh.InspectArtifact(*binary, record.Plan.Review.Reason)
	if err != nil {
		return 1, err
	}
	if artifact.SHA256 != record.Plan.Review.BinarySHA256 || artifact.Size != record.Plan.Review.BinaryBytes || artifact.OS != "linux" || artifact.Arch != record.Plan.Review.Node.Arch {
		return 1, model.Err("artifact_changed", "binary differs from original reviewed plan")
	}
	review := map[string]any{"operation": "ship exact reviewed ARM binary to owned Kubernetes Pod", "key": f.Arg(0), "plan": record.Plan, "binary": artifact, "effect": "one bounded exec attempt; lost outcome requires read-only lookup and never resends bytes"}
	if err := a.meshConfirm(review, *yes, *dry); err != nil {
		return 1, err
	}
	if *dry {
		return 0, nil
	}
	file, err := kubeprovision.OpenReviewedBinary(*binary, record.Plan.Review.BinaryBytes, record.Plan.Review.BinarySHA256)
	if err != nil {
		return 1, err
	}
	defer file.Close()
	streamer, err := kubeprovision.NewOwnedExec(record.Plan)
	if err != nil {
		return 1, err
	}
	result, err := p.ShipBinary(ctx, f.Arg(0), file, streamer)
	if printErr := a.print(result); printErr != nil {
		return 1, printErr
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}
