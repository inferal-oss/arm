package kubeprovision

import (
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

// reviewedOriginTransport is beneath client-go's authentication wrappers and
// is installed on the config shared by typed, SPDY and WebSocket clients.
// Redirects must pass it again before the underlying transport can dial. A CA
// bundle can trust several servers; that does not review their other origins.
type reviewedOriginTransport struct {
	next               http.RoundTripper
	scheme, host, port string
}

func originHost(u *url.URL) string {
	host := u.Hostname()
	if address, err := netip.ParseAddr(host); err == nil {
		return address.String()
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return ""
	}
	return strings.ToLower(ascii)
}
func originPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	return "443"
}
func reviewedOriginGuard(server string) (func(http.RoundTripper) http.RoundTripper, error) {
	origin, err := url.Parse(server)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.Opaque != "" {
		return nil, ErrIdentity
	}
	// Copy immutable origin values rather than retaining a caller-owned URL.
	scheme, host, port := origin.Scheme, originHost(origin), originPort(origin)
	if host == "" {
		return nil, ErrIdentity
	}
	return func(next http.RoundTripper) http.RoundTripper {
		return reviewedOriginTransport{next: next, scheme: scheme, host: host, port: port}
	}, nil
}
func (t reviewedOriginTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil || request.URL.Opaque != "" || request.URL.User != nil ||
		request.URL.Scheme != t.scheme || originHost(request.URL) != t.host || originPort(request.URL) != t.port {
		// A refusing RoundTripper still owns closing the request body.
		if request != nil && request.Body != nil {
			_ = request.Body.Close()
		}
		return nil, ErrIdentity
	}
	response, err := t.next.RoundTrip(request)
	if err != nil {
		return response, err
	}
	return boundMetadataResponse(response)
}
