package manager

import (
	"github.com/yrashk/arm/internal/model"
	"net"
)

func (m *Manager) TrackPreparedMeshInfrastructure(address string) error {
	a, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	network := "tcp6"
	if a.IP.To4() != nil {
		network = "tcp4"
	}
	keep := m.infrastructure[:0]
	for _, entry := range m.infrastructure {
		if entry.Name != "arm-mesh-setup" {
			keep = append(keep, entry)
		}
	}
	m.infrastructure = keep
	m.infrastructure = append(m.infrastructure, model.PortBinding{PortClaim: model.PortClaim{Name: "arm-mesh-setup", Network: network, Address: a.IP.String(), Port: a.Port, Mode: "hold", Namespace: m.portNamespace}, State: "infrastructure", Detail: "Reserved for reviewed mesh setup; bound but not listening"})
	return nil
}
func (m *Manager) RemovePreparedMeshInfrastructure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := m.infrastructure[:0]
	for _, entry := range m.infrastructure {
		if entry.Name != "arm-mesh-setup" {
			keep = append(keep, entry)
		}
	}
	m.infrastructure = keep
}
