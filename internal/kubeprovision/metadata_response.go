package kubeprovision

import (
	"errors"
	"io"
	"net/http"
)

// Bound metadata before client-go's typed JSON/protobuf decoder. A bounded
// dashboard projection or ListOptions.Limit does not bound ignored metadata.
// Upgraded exec/forward streams retain their original duplex body and limits.
const maxMetadataResponseBytes int64 = 8 << 20

var errMetadataResponseTooLarge = errors.New("Kubernetes metadata response exceeds 8 MiB")

type metadataResponseBody struct {
	io.ReadCloser
	remaining int64
	tooLarge  bool
}

func (b *metadataResponseBody) Read(p []byte) (int, error) {
	if b.tooLarge {
		return 0, errMetadataResponseTooLarge
	}
	if len(p) == 0 {
		return 0, nil
	}
	if b.remaining == 0 {
		var probe [1]byte
		n, err := b.ReadCloser.Read(probe[:])
		if n > 0 {
			b.tooLarge = true
			return 0, errMetadataResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func boundMetadataResponse(response *http.Response) (*http.Response, error) {
	if response == nil || response.Body == nil || response.StatusCode == http.StatusSwitchingProtocols {
		return response, nil
	}
	if response.ContentLength > maxMetadataResponseBytes {
		_ = response.Body.Close()
		return nil, errMetadataResponseTooLarge
	}
	response.Body = &metadataResponseBody{ReadCloser: response.Body, remaining: maxMetadataResponseBytes}
	return response, nil
}
