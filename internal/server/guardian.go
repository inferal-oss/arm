package server

import (
	"time"

	"getarm.org/internal/guardian"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
)

// startGuardian builds the host guardian from configuration. A disabled or
// undecided configuration still answers status reads with mode off.
func (s *Server) startGuardian(home string, settings *model.GuardianSettings, m *manager.Manager) (*guardian.Runtime, error) {
	return guardian.New(guardian.FromSettings(settings), home, guardian.Hooks{
		Pressure: func() (model.MonitorPressure, time.Time, bool, guardian.Host) {
			page, _, err := s.monitor.Read(model.MonitorQuery{})
			if err != nil || page.Latest == nil {
				return model.MonitorPressure{}, time.Time{}, true, guardian.Host{}
			}
			h := guardian.Host{CPUs: len(page.Latest.Cores)}
			if v := page.Latest.Memory.Total.Value; v != nil && *v > 0 {
				h.MemoryTotal = uint64(*v)
			}
			return page.Latest.Pressure, page.Latest.At, page.Stale, h
		},
		Managed:  m.GuardianManaged,
		Contacts: m.GuardianContacts,
		Notify:   m.GuardianNotify,
		Ask:      m.GuardianAsk,
	})
}

func (s *Server) guardianStatus() model.GuardianStatus {
	if s.guardian == nil {
		return model.GuardianStatus{Mode: model.GuardianOff, Stale: true, Groups: []model.GuardianGroup{}, Incidents: []model.GuardianIncident{}, Actions: []model.GuardianAction{}}
	}
	return s.guardian.Status()
}
