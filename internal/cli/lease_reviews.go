package cli

import (
	"errors"
	"fmt"
	"github.com/yrashk/arm/internal/model"
	"time"
)

// Root command/help glue is supplied as a separate reviewed hunk; no installer
// or agent CLI is invoked by lease reviews.
func (a *app) leaseReviews(args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("use arm reviews list, inspect ID, policy LEASE, configure LEASE or reply ID")
	}
	op := args[0]
	f := a.flags("reviews " + op)
	f.Bool("json", false, "JSON output (automatic when redirected)")
	var q any
	var result any
	switch op {
	case "list":
		v := &model.LeaseReviewQuery{}
		f.Int64Var(&v.After, "after", 0, "page cursor from the previous response")
		f.StringVar(&v.Agent, "agent", "", "filter exact contact ID")
		f.StringVar(&v.Lease, "lease", "", "filter lease ID")
		f.BoolVar(&v.Pending, "pending", false, "only pending reviews")
		q = v
		result = &model.LeaseReviewPage{}
	case "inspect":
		q = &struct {
			ID string `json:"id"`
		}{}
		result = &model.LeaseReview{}
	case "policy":
		q = &struct {
			Lease string `json:"lease"`
		}{}
		result = &model.LeaseReviewPolicy{}
	case "configure":
		v := &model.LeaseReviewConfigure{Enabled: true}
		f.IntVar(&v.Revision, "revision", 0, "required current lease revision")
		f.DurationVar(&v.Interval, "interval", 10*time.Minute, "review cadence (15s..7d)")
		f.DurationVar(&v.ContentionInterval, "contention-interval", 0, "optional faster cadence while overlapping work waits")
		f.DurationVar(&v.ReplyTimeout, "reply-timeout", time.Minute, "reply window (5s..1h), capped by lease expiry")
		off := f.Bool("off", false, "disable optional reviews")
		generation := f.Int64("policy-generation", -1, "required exact policy generation; inspect policy first (zero if never configured)")
		q = v
		result = &model.LeaseReviewPolicy{}
		if err := a.parse(f, args[1:], false); err != nil {
			return 2, err
		}
		if f.NArg() != 1 {
			return 2, fmt.Errorf("configure requires one lease ID")
		}
		v.Lease = f.Arg(0)
		v.Enabled = !*off
		if *generation < 0 {
			return 2, fmt.Errorf("--policy-generation is required; read arm reviews policy LEASE first")
		}
		v.Generation = generation
		if v.Revision <= 0 {
			return 2, fmt.Errorf("--revision must be the current positive lease revision")
		}
		if err := a.call("lease_review_configure", q, result); err != nil {
			var api *model.Error
			if errors.As(err, &api) {
				return 1, err
			}
			return 1, fmt.Errorf("review policy outcome may be unknown; inspect arm reviews policy %s before changing the request; retain lease revision and policy generation: %w", v.Lease, err)
		}
		return 0, a.printLeaseReview(result)
	case "reply":
		v := &model.LeaseReviewReply{}
		f.StringVar(&v.Agent, "agent", "", "registered contact ID")
		f.StringVar(&v.Session, "session", "", "exact registered session")
		f.StringVar(&v.Registration, "registration", "", "exact registration generation")
		f.StringVar(&v.Intent, "intent", "needed", "needed, release, renew or resize (intent only)")
		f.StringVar(&v.Explanation, "why", "", "required continued need/progress or requested change; never executed")
		q = v
		result = &model.LeaseReview{}
	default:
		return 2, fmt.Errorf("unknown reviews operation %q", op)
	}
	if err := a.parse(f, args[1:], false); err != nil {
		return 2, err
	}
	if op == "list" {
		if f.NArg() != 0 {
			return 2, fmt.Errorf("list accepts no positional arguments")
		}
	} else {
		if f.NArg() != 1 {
			return 2, fmt.Errorf("%s requires one ID", op)
		}
		switch v := q.(type) {
		case *model.LeaseReviewReply:
			v.ID = f.Arg(0)
			if v.Agent == "" || v.Session == "" || v.Registration == "" || v.Explanation == "" {
				return 2, fmt.Errorf("reply requires --agent, --session, --registration and --why")
			}
		case *struct {
			ID string `json:"id"`
		}:
			v.ID = f.Arg(0)
		case *struct {
			Lease string `json:"lease"`
		}:
			v.Lease = f.Arg(0)
		}
	}
	operation := map[string]string{"list": "lease_review_list", "inspect": "lease_review_inspect", "policy": "lease_review_policy", "reply": "lease_review_reply"}[op]
	if err := a.call(operation, q, result); err != nil {
		return 1, err
	}
	return 0, a.printLeaseReview(result)
}

func (a *app) printLeaseReview(v any) error {
	if a.machine() {
		return a.print(v)
	}
	switch x := v.(type) {
	case *model.LeaseReviewPage:
		fmt.Fprintln(a.out, a.paint("1;36", "Lease reviews"))
		if x.SchedulerError != "" {
			fmt.Fprintln(a.out, "Review scheduler: "+terminalSafe(x.SchedulerError))
		}
		if len(x.Items) == 0 {
			fmt.Fprintln(a.out, "No matching retained reviews.")
		}
		for _, r := range x.Items {
			fmt.Fprintf(a.out, "%s  %s  %s  %d lease(s)  reply by %s\n", terminalSafe(r.ID), a.paint("1", terminalSafe(r.State)), terminalSafe(r.Agent), len(r.Context), r.Deadline.Local().Format(time.RFC3339))
		}
		if x.More {
			fmt.Fprintf(a.out, "More reviews: arm reviews list --after %d\n", x.Cursor)
		}
	case *model.LeaseReviewPolicy:
		if !x.Enabled {
			fmt.Fprintf(a.out, "Lease %s: optional reviews are off (policy generation %d).\n", terminalSafe(x.Lease), x.Generation)
			return nil
		}
		fmt.Fprintf(a.out, "%s for lease %s\nPolicy generation %d; every %s; reply window %s (capped by lease expiry).\n", a.paint("1;32", "Optional reviews enabled"), terminalSafe(x.Lease), x.Generation, x.Interval, x.ReplyTimeout)
		if x.ContentionInterval > 0 {
			fmt.Fprintf(a.out, "While overlapping work waits: every %s.\n", x.ContentionInterval)
		}
	case *model.LeaseReview:
		fmt.Fprintf(a.out, "%s  %s\nAgent %s · session %s · registration %s\nReply by %s\n", a.paint("1;36", terminalSafe(x.ID)), a.paint("1", terminalSafe(x.State)), terminalSafe(x.Agent), terminalSafe(x.Session), terminalSafe(x.Registration), x.Deadline.Local().Format(time.RFC3339))
		for _, c := range x.Context {
			fmt.Fprintf(a.out, "\n%s · revision %d\nWhy: %s\nCPU %.2f cores · memory %d bytes · disk %d bytes\nExpires %s · %d queued request(s) overlap a resource kind\nUsage: CPU %s · memory %s · stale %t · complete %t\n", terminalSafe(c.Lease), c.Revision, terminalSafe(c.Purpose), c.Resources.CPU, c.Resources.Memory, c.Resources.Disk, c.Expires.Local().Format(time.RFC3339), c.QueuedDemand, terminalSafe(c.Usage.CPUQuality), terminalSafe(c.Usage.MemoryQuality), c.Usage.Stale, c.Usage.Complete)
		}
		if x.Reply != nil {
			fmt.Fprintf(a.out, "\nAgent intent: %s\nExplanation: %s\n", terminalSafe(x.Reply.Intent), terminalSafe(x.Reply.Explanation))
		}
		if x.Detail != "" {
			fmt.Fprintln(a.out, terminalSafe(x.Detail))
		}
		fmt.Fprintln(a.out, "\nReviews never renew leases or establish compliance. Use ordinary revision-checked lease actions separately.")
	default:
		return a.print(v)
	}
	return nil
}
