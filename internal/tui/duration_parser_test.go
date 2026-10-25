package tui

import (
	"testing"
	"time"
)

func TestDurationParserAcceptsGeneratedPositiveStrings(t *testing.T) {
	for _, want := range []time.Duration{10 * time.Minute, 2 * time.Minute, time.Hour, 24 * time.Hour, 7 * 24 * time.Hour} {
		text := want.String()
		got, err := parseTTL(text)
		if err != nil || got != want {
			t.Fatal("positive Go-generated duration refused", text, got, err)
		}
	}
	for _, text := range []string{"0h10m0s", "0s1m0s", "0.1m0s"} {
		if got, err := parseTTL(text); err != nil || got <= 0 {
			t.Fatal("positive duration with zero components refused", text, got, err)
		}
	}
	for _, text := range []string{"", "0s", "0h0m0s", "-10m0s", "+0h1m", "7d0.000000001s", "999999999999999999h0s", "0.0000000001s", "10m0s trailing"} {
		if _, err := parseTTL(text); err == nil {
			t.Fatal("invalid/zero/overflow duration accepted", text)
		}
	}
}

func TestDurationParserGeneratedValueReachesOrdinaryExtensionReview(t *testing.T) {
	m, transport := fixtureDashboard(t)
	lease := activeLease("zero-component-review")
	expires := time.Now().Add(time.Minute)
	lease.Expires = &expires
	m.detail = &lease
	m.startReview("renew")
	m.input.SetValue((10 * time.Minute).String())
	press(m, "enter")
	if m.review == nil || m.review.request.TTL != 10*time.Minute || m.review.request.ID != lease.ID || m.review.request.Revision != lease.Revision {
		t.Fatal("Go-generated duration cannot reach exact extension review", m.notice)
	}
	if len(transport.operations) != 0 {
		t.Fatal("editing/reviewing duration dispatched a resource mutation", transport.operations)
	}
}
