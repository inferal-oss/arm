package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/model"
)

func TestLeaseReviewCLIRejectsMissingIdentityBeforeBackend(t *testing.T) {
	for _, args := range [][]string{{"configure", "lease"}, {"reply", "review"}, {"list", "extra"}, {"inspect"}, {"unknown"}} {
		var out, stderr bytes.Buffer
		a := app{ctx: context.Background(), home: t.TempDir(), out: &out, err: &stderr}
		code, err := a.leaseReviews(args)
		if err == nil || code != 2 {
			t.Fatal(args, code, err)
		}
		if strings.Contains(err.Error(), "endpoint") {
			t.Fatal("invalid arguments reached backend", err)
		}
	}
}
func TestLeaseReviewCLIHumanExplainsIntentAndMachineIsStructured(t *testing.T) {
	var out bytes.Buffer
	a := app{out: &out, outputTTY: true, colors: false}
	x := &model.LeaseReview{ID: "review", State: "replied", Agent: "owner", Reply: &model.LeaseReviewReply{Intent: "renew", Explanation: "still linking"}}
	if err := a.printLeaseReview(x); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "still linking") || !strings.Contains(out.String(), "never renew leases") {
		t.Fatal(out.String())
	}
	out.Reset()
	a.outputTTY = false
	if err := a.printLeaseReview(x); err != nil || !strings.Contains(out.String(), `"reply"`) {
		t.Fatal(out.String(), err)
	}
}

func TestLeaseReviewCLIRoundTripWithPrivateBackend(t *testing.T) {
	home := dashboardTestBackend(t)
	var out, stderr bytes.Buffer
	code := Main([]string{"--home", home, "reviews", "list", "--pending", "--json"}, strings.NewReader(""), &out, &stderr)
	if code != 0 || !strings.Contains(out.String(), `"items": []`) {
		t.Fatal(code, out.String(), stderr.String())
	}
	for _, args := range [][]string{{"reviews", "--help"}, {"reviews", "configure", "--help"}, {"reviews", "reply", "--help"}} {
		out.Reset()
		stderr.Reset()
		if code = Main(args, strings.NewReader(""), &out, &stderr); code != 0 {
			t.Fatal(args, code, stderr.String())
		}
		if !strings.Contains(out.String(), "reviews") {
			t.Fatal("missing scoped help", out.String())
		}
	}
}

func TestLeaseReviewTerminalEscapesAgentExplanation(t *testing.T) {
	var out bytes.Buffer
	a := app{out: &out, outputTTY: true, colors: false}
	x := &model.LeaseReview{ID: "review", Reply: &model.LeaseReviewReply{Intent: "needed", Explanation: "why\x1b[2J\nnext"}}
	if err := a.printLeaseReview(x); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Fatal("agent controls escaped renderer", out.String())
	}
	if !strings.Contains(out.String(), `\u001b[2J\nnext`) {
		t.Fatal("explanation lost instead of escaped", out.String())
	}
}
