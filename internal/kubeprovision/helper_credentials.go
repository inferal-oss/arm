package kubeprovision

import (
	"errors"

	"k8s.io/client-go/rest"
)

// External kubeconfig credential plugins can launch descendants outside the
// helper's original process group. Until those descendants have an exact
// ownership witness, owned stream effects refuse such configurations before
// the first API request. Read-only context discovery remains separate.
func checkOwnedStreamCredentials(config *rest.Config) error {
	if config == nil || config.ExecProvider != nil || config.AuthProvider != nil {
		return errors.New("external Kubernetes credential helper unsupported for owned streams")
	}
	return nil
}
