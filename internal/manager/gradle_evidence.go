package manager

import (
	"fmt"
	"os"

	"github.com/yrashk/arm/internal/model"
)

func (m *Manager) openGradleChild(r gradleRecord, name string) (*os.File, error) {
	if r.Identity == nil {
		return nil, model.Err("invalid_evidence", "Gradle workspace identity was not committed")
	}
	root, _, err := m.openGradleDirectory(r.Storage, r.Name, r.Identity, false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	id, ok := r.Children[name]
	if !ok {
		return nil, model.Err("invalid_evidence", "Gradle control subdirectory was not recorded")
	}
	child, _, err := gradleChildDirectory(root, name, &id, false)
	return child, err
}

func (m *Manager) checkGradleDirectories(r gradleRecord) error {
	if r.Identity == nil {
		return model.Err("invalid_evidence", "Gradle private directory was not identified")
	}
	root, _, err := m.openGradleDirectory(r.Storage, r.Name, r.Identity, false)
	if err != nil {
		return err
	}
	defer root.Close()
	for name, id := range r.Children {
		child, _, err := gradleChildDirectory(root, name, &id, false)
		if err != nil {
			return err
		}
		child.Close()
	}
	return nil
}

func (m *Manager) gradleEvidence(r gradleRecord, phase string) (gradleEvidence, error) {
	var e gradleEvidence
	dir, err := m.openGradleChild(r, "receipts")
	if err != nil {
		return e, err
	}
	defer dir.Close()
	if err := gradleReadJSON(dir, phase+".json", &e); err != nil {
		return e, err
	}
	if e.Lease != r.Lease || e.Nonce != r.Nonce || e.Phase != phase || e.PID <= 0 || e.StartMillis <= 0 || len(e.Detail) > 4096 {
		return e, model.Err("invalid_evidence", "Gradle receipt identity differs from its durable operation")
	}
	if phase != "mismatch" {
		if e.PID != r.ExpectedPID || e.JavaHome != r.Binding.JavaHome || e.GradleHome != r.Binding.Home || e.Workers != r.Binding.WorkerLimit || e.MaxHeap <= 0 || e.MaxHeap > r.ServiceHeap || e.MetaspaceMax <= 0 || e.MetaspaceMax > r.ServiceMetaspace || e.Version == "" || len(e.Version) > 64 || (r.Binding.ServiceStartMillis != 0 && e.StartMillis != r.Binding.ServiceStartMillis) || (r.Binding.GradleVersion != "" && e.Version != r.Binding.GradleVersion) {
			return e, model.Err("invalid_evidence", "Gradle receipt daemon/JDK/home/worker/version/heap evidence differs from the owned service")
		}
	}
	return e, nil
}

func (m *Manager) gradleFinishedEvidence(r gradleRecord) (gradleEvidence, error) {
	if mismatch, err := m.gradleEvidence(r, "mismatch"); err == nil {
		return mismatch, model.Err("service_mismatch", fmt.Sprintf("Gradle observed an unexpected JVM (%d): %s; no signal authority was inferred", mismatch.PID, mismatch.Detail))
	} else if !os.IsNotExist(err) {
		return gradleEvidence{}, err
	}
	started, err := m.gradleEvidence(r, "started")
	if err != nil {
		return started, err
	}
	finished, err := m.gradleEvidence(r, "finished")
	if err != nil {
		return finished, err
	}
	if started.PID != finished.PID || started.StartMillis != finished.StartMillis || started.Version != finished.Version || started.JavaHome != finished.JavaHome || started.GradleHome != finished.GradleHome {
		return finished, model.Err("invalid_evidence", "Gradle start/completion receipts refer to different daemon instances")
	}
	return finished, nil
}
