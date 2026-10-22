package mesh

import (
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/yrashk/arm/internal/model"
)

// StartupPolicy records configured ceilings, never observed free memory. A
// missing record is distinct from current defaults or historical settings.
type StartupPolicy struct {
	Schema     int             `json:"schema"`
	Resources  model.Resources `json:"resources"`
	Listen     string          `json:"listen"`
	RecordedAt time.Time       `json:"recorded_at,omitempty"`
}

func (p StartupPolicy) Validate() error {
	host, port, err := net.SplitHostPort(p.Listen)
	ip := net.ParseIP(host)
	number, portErr := strconv.Atoi(port)
	if p.Schema != 1 || err != nil || portErr != nil || number < 0 || number > 65535 || ip == nil || !ip.IsLoopback() || math.IsNaN(p.Resources.CPU) || math.IsInf(p.Resources.CPU, 0) || p.Resources.CPU <= 0 || p.Resources.Memory < 0 || p.Resources.Disk < 0 {
		return fmt.Errorf("invalid configured ARM startup policy")
	}
	// Resolve validates the numeric port, including the intentional port0.
	if _, err := net.ResolveTCPAddr("tcp", p.Listen); err != nil {
		return fmt.Errorf("invalid native startup endpoint: %w", err)
	}
	return nil
}

func ReadStartupPolicy(home string) (StartupPolicy, bool, error) {
	var result StartupPolicy
	raw, err := privateFile(filepath.Join(home, "daemon-launch.json"))
	if os.IsNotExist(err) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if err := Decode(raw, &result); err != nil {
		return result, false, err
	}
	if err := result.Validate(); err != nil {
		return result, false, err
	}
	return result, true, nil
}

func SaveStartupPolicy(home string, p StartupPolicy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	p.RecordedAt = time.Now().UTC()
	return save(filepath.Join(home, "daemon-launch.json"), p)
}
