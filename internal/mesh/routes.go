package mesh

import (
	"fmt"
	"net"
	"slices"
)

func PrivateInterfaces() ([]string, error) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	result := []string{}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && privateRouteIP(ip) {
			raw := ip.String()
			if !slices.Contains(result, raw) {
				result = append(result, raw)
				if len(result) > 64 {
					return nil, fmt.Errorf("too many private interface addresses for bounded mesh setup")
				}
			}
		}
	}
	slices.Sort(result)
	return result, nil
}
func privateRouteIP(ip net.IP) bool {
	return ip != nil && ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsMulticast() && !ip.IsUnspecified()
}

// SyntheticTarget is a route adapter for an authenticated SSH gateway reporting
// loopback/port0. Reported assigned addresses are locators, never identities.
func SyntheticTarget(route SSHRoute, assigned, dns []string) (string, error) {
	if !route.Synthetic || !net.ParseIP(route.ClientIP).IsLoopback() || !net.ParseIP(route.ServerIP).IsLoopback() {
		return "", fmt.Errorf("fallback requires explicit synthetic loopback SSH route")
	}
	if len(assigned) == 0 || len(assigned) > 64 || len(dns) > 64 {
		return "", fmt.Errorf("private interface/DNS candidates absent or exceed bound")
	}
	unique := []string{}
	for _, raw := range assigned {
		ip := net.ParseIP(raw)
		if !privateRouteIP(ip) {
			return "", fmt.Errorf("invalid private assigned route candidate")
		}
		value := ip.String()
		if !slices.Contains(unique, value) {
			unique = append(unique, value)
		}
	}
	intersection := []string{}
	for _, raw := range dns {
		ip := net.ParseIP(raw)
		if privateRouteIP(ip) && slices.Contains(unique, ip.String()) && !slices.Contains(intersection, ip.String()) {
			intersection = append(intersection, ip.String())
		}
	}
	if len(intersection) == 1 {
		return intersection[0], nil
	}
	if len(intersection) > 1 {
		return "", fmt.Errorf("multiple DNS/assigned interface matches; specify exact local/remote interface IPs")
	}
	ipv4, ipv6 := []string{}, []string{}
	for _, raw := range unique {
		if net.ParseIP(raw).To4() != nil {
			ipv4 = append(ipv4, raw)
		} else {
			ipv6 = append(ipv6, raw)
		}
	}
	if len(ipv4) == 1 {
		return ipv4[0], nil
	}
	if len(ipv4) > 1 {
		return "", fmt.Errorf("multiple assigned private IPv4 interfaces; specify exact local/remote interface IPs")
	}
	if len(ipv6) == 1 {
		return ipv6[0], nil
	}
	return "", fmt.Errorf("no unique assigned private route; specify exact local/remote interface IPs")
}

// RouteSource asks the kernel for routing only. UDP connect sends no datagram;
// no Write is performed and the socket is closed before returning.
func RouteSource(target string) (string, error) {
	ip := net.ParseIP(target)
	if !privateRouteIP(ip) {
		return "", fmt.Errorf("route target must be an assigned private host address")
	}
	network := "udp6"
	if ip.To4() != nil {
		network = "udp4"
	}
	socket, err := net.DialUDP(network, nil, &net.UDPAddr{IP: ip, Port: 9})
	if err != nil {
		return "", err
	}
	defer socket.Close()
	source := socket.LocalAddr().(*net.UDPAddr).IP
	if !privateRouteIP(source) || !InterfaceIP(source.String()) {
		return "", fmt.Errorf("kernel-selected source is not a private assigned interface")
	}
	return source.String(), nil
}
