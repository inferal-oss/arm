package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"syscall"
	"time"

	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
	"getarm.org/internal/ports"
)

func (m *Manager) provisionPorts(l model.Lease, run *RunRequest, leases []model.Lease) (model.Lease, error) {
	if reason := m.startDeadlineProblem(l); reason != nil {
		return m.rejectUnstarted(l, reason)
	}
	blocked := append([]model.PortBinding{}, m.infrastructure...)
	for _, other := range leases {
		if other.ID != l.ID && consuming(other.State) {
			blocked = append(blocked, other.Ports...)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sockets, err := ports.Open(ctx, l.Request.Ports, blocked)
	if err != nil {
		if l.State == "queued" && l.Failure == err.Error() {
			return l, nil
		}
		l.Failure = err.Error()
		l.Revision++
		l.State = "terminal"
		l.Outcome = "admission_failed"
		now := time.Now().UTC()
		l.Ended = &now
		if l.Request.Queue {
			l.State = "queued"
			l.Outcome = ""
			l.Started = nil
			l.Expires = nil
			l.Clock = nil
			l.Ended = nil
		}
		if e := m.put(l, "ports.admission_failed"); e != nil {
			return l, e
		}
		if l.State == "queued" {
			return l, nil
		}
		return l, model.Err("port_unavailable", fmt.Sprintf("lease %s: %v", l.ID, err))
	}
	if reason := m.startDeadlineProblem(l); reason != nil {
		ports.Close(sockets)
		return m.rejectUnstarted(l, reason)
	}
	// Binding can take time. The usable TTL begins when the whole bundle is
	// selected, including the first immediate grant as well as queued grants.
	if err := m.activate(&l, run != nil); err != nil {
		ports.Close(sockets)
		return l, err
	}
	l.Failure = ""
	l.Ports = []model.PortBinding{}
	for _, s := range sockets {
		l.Ports = append(l.Ports, s.Binding)
	}
	l.State = "active"
	if run != nil {
		l.State = "provisioning"
	}
	l.Capabilities = append(append([]string{}, l.Capabilities...), "port-reservation")
	for _, s := range sockets {
		if s.File != nil {
			l.Capabilities = append(l.Capabilities, "socket-activation")
			break
		}
	}
	l.Revision++
	if err = m.put(l, "ports.bound"); err != nil {
		ports.Close(sockets)
		return l, err
	}
	m.portSockets[l.ID] = sockets
	if run != nil {
		return m.launch(l, *run)
	}
	return l, nil
}

func portConsumers(ls []model.Lease, id string) []string {
	consumers := []string{}
	for _, l := range ls {
		if l.Request.PortLease == id && (consuming(l.State) || l.State == "queued") {
			consumers = append(consumers, l.ID)
		}
	}
	return consumers
}
func (m *Manager) portParent(r model.Request, consumer string) (model.Lease, error) {
	l, err := m.get(r.PortLease)
	if err != nil {
		return l, err
	}
	if kubesource.IsReserved(l.Request) {
		return l, model.Err("ports_unavailable", "native source ports cannot be attached by ordinary work")
	}
	if l.Request.Kind != "ports" || l.Owner != m.owner || l.Host != m.host || l.State != "active" || l.Expires == nil || !m.leaseCovers(l, r.TTL) || len(l.Ports) == 0 || len(m.portSockets[l.ID]) != len(l.Ports) {
		return l, model.Err("ports_unavailable", "parent ports must be active on this host/user, still supervised, and outlast the command's TTL")
	}
	ls, err := m.list()
	if err != nil {
		return l, err
	}
	for _, id := range portConsumers(ls, l.ID) {
		if id != consumer {
			return l, model.Err("ports_in_use", "a port lease permits only one supervised consumer at a time")
		}
	}
	return l, nil
}
func (m *Manager) activatePorts(l *model.Lease, cmd *exec.Cmd) error {
	id := l.ID
	if l.Request.PortLease != "" {
		parent, err := m.portParent(l.Request, l.ID)
		if err != nil {
			return err
		}
		id = parent.ID
	}
	sockets := m.portSockets[id]
	if len(sockets) == 0 {
		return model.Err("ports_unavailable", "no supervised sockets remain for this lease")
	}
	activated := []model.ActivatedPort{}
	for _, s := range sockets {
		p := model.ActivatedPort{PortBinding: s.Binding}
		if s.File != nil {
			fd := 3 + len(cmd.ExtraFiles)
			p.FD = &fd
			p.Mode = "activation"
			cmd.ExtraFiles = append(cmd.ExtraFiles, s.File)
		} else if s.Binding.Mode != "cooperative" {
			return model.Err("ports_unavailable", "held descriptor is unavailable")
		}
		activated = append(activated, p)
	}
	raw, err := json.Marshal(activated)
	if err != nil {
		return err
	}
	cmd.Env = append(cmd.Env, "ARM_PORTS="+string(raw), "ARM_PORT_LEASE="+id)
	return nil
}

// finishPorts only closes descriptors ARM owns, then probes exact bindings.
// Occupancy is uncertainty about reclamation, never permission to kill a PID.
func (m *Manager) finishPorts(l *model.Lease) {
	ports.Close(m.portSockets[l.ID])
	delete(m.portSockets, l.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for i := range l.Ports {
		b := &l.Ports[i]
		b.SocketID = ""
		b.State = "observed_available"
		b.Detail = "Exact bind probe succeeded; this is not a future reservation"
		if err := ports.Probe(ctx, *b); err != nil {
			b.State = "unresolved"
			b.Detail = err.Error()
			l.State = "needs_attention"
			l.Ended = nil
			l.Failure = "Port reclamation unresolved: " + err.Error()
		}
	}
}
func (m *Manager) endPortParent(l model.Lease, outcome string) (model.Lease, error) {
	l.State = "ending"
	l.Outcome = outcome
	l.Revision++
	if err := m.put(l, "ports.ending"); err != nil {
		return l, err
	}
	ls, err := m.list()
	if err != nil {
		return l, err
	}
	for _, id := range portConsumers(ls, l.ID) {
		_, _ = m.end(id, "revoked")
	}
	return m.finishPortParent(l)
}
func (m *Manager) finishPortParent(l model.Lease) (model.Lease, error) {
	ls, err := m.list()
	if err != nil {
		return l, err
	}
	if len(portConsumers(ls, l.ID)) > 0 {
		for _, c := range ls {
			if c.Request.PortLease == l.ID && c.State == "needs_attention" {
				l.State = "needs_attention"
				l.Failure = "Attached command " + c.ID + " requires reconciliation"
				l.Revision++
				return l, m.put(l, "ports.needs_attention")
			}
		}
		return l, nil
	}
	l.State = "terminal"
	now := time.Now().UTC()
	l.Ended = &now
	l.Revision++
	m.finishPorts(&l)
	return l, m.put(l, "ports."+l.State)
}
func (m *Manager) reconcilePorts(l model.Lease) (model.Lease, error) {
	if l.PID > 0 && !errors.Is(syscall.Kill(-l.PID, 0), syscall.ESRCH) {
		return l, model.Err("needs_attention", "process group remains or cannot be inspected; no signal was sent")
	}
	ls, err := m.list()
	if err != nil {
		return l, err
	}
	if len(portConsumers(ls, l.ID)) > 0 {
		return l, model.Err("ports_in_use", "reconcile attached consumers before the port allocation")
	}
	l.State = "terminal"
	now := time.Now().UTC()
	l.Ended = &now
	l.Failure = ""
	l.Revision++
	if l.Outcome == "" || l.Outcome == "port_supervision_lost" {
		l.Outcome = "reconciled"
	}
	m.finishPorts(&l)
	err = m.put(l, "ports.reconciled")
	if err == nil && l.State != "terminal" {
		err = model.Err("needs_attention", "port remains occupied or unavailable; no unrelated process was signalled")
	}
	return l, err
}

// TrackInfrastructure records the already bound backend listener. Its opaque
// resource ID is not a lease ID and ordinary release/reconcile cannot end it.
func (m *Manager) TrackInfrastructure(address net.Addr) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := address.(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("backend listener is not TCP")
	}
	namespace := m.portNamespace
	if m.portErr != nil {
		namespace = "unknown"
	}
	network := "tcp6"
	if a.IP.To4() != nil {
		network = "tcp4"
	}
	m.infrastructure = []model.PortBinding{{PortClaim: model.PortClaim{Name: "arm-backend", Network: network, Address: a.IP.String(), Port: a.Port, Mode: "hold", Namespace: namespace}, State: "infrastructure", Detail: "Protected ARM local API and web backend listener"}}
	return nil
}
func (m *Manager) portResources(s *model.Status, leases []model.Lease) {
	appendRow := func(b model.PortBinding, u model.ResourceUse, accounting string) {
		id := m.host + "/ports/" + b.Namespace + "/" + b.Network + "/" + net.JoinHostPort(b.Address, fmt.Sprint(b.Port))
		s.Resources = append(s.Resources, model.ResourceView{ID: id, Host: m.host, Pool: id, Kind: "port", Unit: "socket-binding", BudgetTotal: model.CountQuantity(1), Reserved: model.CountQuantity(1), Available: model.CountQuantity(0), Accounting: accounting, Port: &b, Users: []model.ResourceUse{u}})
	}
	for _, l := range leases {
		if consuming(l.State) {
			for _, b := range l.Ports {
				appendRow(b, model.ResourceUse{Lease: l.ID, Owner: l.Owner, OwnerUsername: l.OwnerUsername, Agent: l.Request.Agent, Project: l.Request.Project, Reason: l.Request.Reason, Quantity: model.CountQuantity(1), State: l.State, Expires: l.Expires, Relationship: "reservation"}, b.State)
				row := &s.Resources[len(s.Resources)-1]
				for _, c := range leases {
					if c.Request.PortLease == l.ID && consuming(c.State) {
						row.Users = append(row.Users, model.ResourceUse{Lease: c.ID, Owner: c.Owner, OwnerUsername: c.OwnerUsername, Agent: c.Request.Agent, Project: c.Request.Project, Reason: c.Request.Reason, Quantity: model.CountQuantity(0), State: c.State, Expires: c.Expires, Relationship: "consumer"})
					}
				}
			}
		}
	}
	for _, b := range m.infrastructure {
		appendRow(b, model.ResourceUse{Owner: m.owner, OwnerUsername: m.ownerUsername, Reason: b.Detail, Quantity: model.CountQuantity(1), State: "infrastructure"}, "protected-infrastructure")
	}
}

func (m *Manager) DiscoverPorts(ctx context.Context) (model.PortInventory, error) {
	r := ports.Discover(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	r.Host = m.host
	ls, err := m.list()
	if err != nil {
		return r, err
	}
	for i := range r.Sockets {
		s := &r.Sockets[i]
		observed := model.PortClaim{Network: s.Network, Address: s.Address, Port: s.Port, Namespace: r.Namespace, DualStack: s.Address == "::"}
		for _, l := range ls {
			if consuming(l.State) {
				for _, b := range l.Ports {
					if ports.Overlap(observed, b.PortClaim) {
						s.OverlappingLeases = append(s.OverlappingLeases, l.ID)
					}
					if b.SocketID != "" && s.ID == b.SocketID && r.Namespace == b.Namespace && s.Network == b.Network {
						s.Claims = append(s.Claims, l.ID)
					}
				}
			}
		}
	}
	return r, nil
}
