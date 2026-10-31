package meshsshrelay

import (
	"crypto/tls"
	"errors"
	"fmt"
)

// NativeCloseUncertain retains the first original socket close failure.
// Wrapper and TLS close failures remain application errors.
type NativeCloseUncertain struct{ Cause error }

func (e *NativeCloseUncertain) Error() string {
	return fmt.Sprintf("original relay socket close uncertain: %v", e.Cause)
}
func (e *NativeCloseUncertain) Unwrap() error { return e.Cause }

type nativeCloseProver interface{ NativeCloseProof() error }

func closeCarrierOriginal(raw interface{ Close() error }) (error, error) {
	applicationErr := raw.Close()
	prover, ok := raw.(nativeCloseProver)
	if !ok {
		return applicationErr, &NativeCloseUncertain{Cause: errors.New("original relay socket close proof unavailable")}
	}
	if err := prover.NativeCloseProof(); err != nil {
		return applicationErr, &NativeCloseUncertain{Cause: err}
	}
	return applicationErr, nil
}

// CloseOriginalTLS closes the inner TLS stream and returns its application
// close error separately from the first raw original transport close result.
func CloseOriginalTLS(conn *tls.Conn) (error, error) {
	if conn == nil {
		return nil, &NativeCloseUncertain{Cause: errors.New("original relay TLS connection unavailable")}
	}
	applicationErr := conn.Close()
	raw := conn.NetConn()
	// Join the carrier's watcher even when TLS Close returns early. This
	// wrapper Close is application cleanup; NativeCloseProof below reads the
	// cached first raw socket result rather than trusting a later Close.
	applicationErr = errors.Join(applicationErr, raw.Close())
	prover, ok := raw.(nativeCloseProver)
	if !ok {
		return applicationErr, &NativeCloseUncertain{Cause: errors.New("original relay socket close proof unavailable")}
	}
	if err := prover.NativeCloseProof(); err != nil {
		return applicationErr, &NativeCloseUncertain{Cause: err}
	}
	return applicationErr, nil
}
