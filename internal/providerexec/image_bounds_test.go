package providerexec

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/providerproto"
)

func TestImageAbsoluteAndRoleByteBoundsBeforeNativeIO(t *testing.T) {
	for _, limit := range []int64{math.MinInt64, -1, 0, MaxRuntimeBytes + 1, math.MaxInt64} {
		if _, err := pinImage("relative-never-opened", limit); err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatal("invalid hash bound reached native/path access", limit, err)
		}
	}
	for _, size := range []int64{math.MinInt64, -1, 0, MaxRuntimeBytes + 1, math.MaxInt64} {
		i := Image{valid: true, path: "relative-never-opened", size: size}
		if err := i.check(); err == nil || !strings.Contains(err.Error(), "role byte bound") {
			t.Fatal("invalid saved size reached native/path access", size, err)
		}
	}
	for _, size := range []int64{1, MaxImageBytes, MaxRuntimeBytes} {
		if !imageSizeWithin(size, MaxRuntimeBytes) {
			t.Fatal("valid runtime boundary refused", size)
		}
	}
	for _, size := range []int64{1, MaxImageBytes} {
		if !imageSizeWithin(size, MaxImageBytes) {
			t.Fatal("valid provider boundary refused", size)
		}
	}
	runtime := Image{valid: true, size: MaxRuntimeBytes}
	provider := Image{valid: true, size: MaxImageBytes}
	if _, err := SelectFromTrusted(runtime, provider); err != nil {
		t.Fatal("exact role boundary refused", err)
	}
	for _, size := range []int64{math.MinInt64, -1, 0, MaxImageBytes + 1, MaxRuntimeBytes, math.MaxInt64} {
		provider.size = size
		if _, err := SelectFromTrusted(runtime, provider); err == nil {
			t.Fatal("runtime allowance inherited as provider", size)
		}
		packet := provider.packet()
		if _, err := SelectFromTrusted(runtime.packet().image(), packet.image()); err == nil {
			t.Fatal("reconstructed private packet inherited provider allowance", size)
		}
		if err := packet.image().checkWithin(MaxImageBytes); err == nil || !strings.Contains(err.Error(), "role byte bound") {
			t.Fatal("oversize provider reached native open/hash", size, err)
		}
	}
}

func TestActualSmallNativeImagesKeepBothRoles(t *testing.T) {
	f := newFixture(t)
	if f.runtime.size > MaxImageBytes || f.provider.size > MaxImageBytes {
		t.Fatal("expected actual bounded small fixture")
	}
	if err := f.runtime.checkWithin(MaxRuntimeBytes); err != nil {
		t.Fatal(err)
	}
	if err := f.provider.checkWithin(MaxImageBytes); err != nil {
		t.Fatal(err)
	}
	p := f.prepared(t, "bounds-positive", "registration", providerproto.Validate, "")
	// Even an otherwise trusted detached prepared selection must retain role
	// sizes. No huge image allocation/read is necessary to prove this refusal.
	over := p.selected
	over.provider.size = MaxImageBytes + 1
	q, err := providerproto.DecodeRequest(p.request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Prepare("bounds-over", "registration", over, p.held, q.Request(), p.original, time.Second); err == nil {
		t.Fatal("Prepare bypassed provider role bound")
	}
	h := f.begin(t, p)
	f.grant(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	o, err := h.WaitObservation(ctx)
	if err != nil || !o.ProtocolSuccess || !o.ScopedNativeClosed {
		t.Fatal("small-image actual helper refused", o, err)
	}
	f.settle(t, h)
}
