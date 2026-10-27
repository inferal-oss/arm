package ports

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func Discover(ctx context.Context) model.PortInventory {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ns, err := Namespace()
	r := model.PortInventory{Namespace: ns, At: time.Now().UTC(), Sockets: []model.SocketObservation{}, Complete: true, AttributionComplete: true}
	if err != nil {
		r.Complete = false
		r.AttributionComplete = false
		r.Detail = err.Error()
		return r
	}
	discover(ctx, &r)
	if ctx.Err() != nil {
		r.Complete = false
		r.AttributionComplete = false
		r.Detail += "; observation deadline reached"
	}
	sort.Slice(r.Sockets, func(i, j int) bool {
		a, b := r.Sockets[i], r.Sockets[j]
		if a.Network != b.Network {
			return a.Network < b.Network
		}
		if a.Address != b.Address {
			return a.Address < b.Address
		}
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		return a.ID < b.ID
	})
	for i := range r.Sockets {
		s := &r.Sockets[i]
		if s.Owners == nil {
			s.Owners = []model.SocketOwner{}
		}
		s.Claims = []string{}
		s.OverlappingLeases = []string{}
		sort.Slice(s.Owners, func(i, j int) bool { return s.Owners[i].PID < s.Owners[j].PID })
	}
	return r
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > (1<<20)-b.Len() {
		return 0, fmt.Errorf("socket inventory output limit exceeded")
	}
	return b.Buffer.Write(p)
}
