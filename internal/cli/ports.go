package cli

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/yrashk/arm/internal/model"
)

type portFlags []model.PortClaim

func (p *portFlags) String() string { return fmt.Sprint([]model.PortClaim(*p)) }
func (p *portFlags) Set(value string) error {
	name := ""
	if before, after, found := strings.Cut(value, "="); found {
		name, value = before, after
	}
	network, endpoint, found := strings.Cut(value, "://")
	if !found {
		return fmt.Errorf("port requires [NAME=]NETWORK://IP:PORT[-END]")
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return err
	}
	start, end, hasRange := strings.Cut(port, "-")
	n, err := strconv.Atoi(start)
	if err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}
	c := model.PortClaim{Name: name, Network: network, Address: host, Port: n}
	if hasRange {
		c.RangeEnd, err = strconv.Atoi(end)
		if err != nil || c.RangeEnd == 0 {
			return fmt.Errorf("invalid port range end %q", end)
		}
	}
	*p = append(*p, c)
	return nil
}

func (a *app) ports(args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("ports requires reserve, list or discover")
	}
	if args[0] == "reserve" {
		return a.acquire("ports", args[1:])
	}
	f := a.flags("ports " + args[0])
	f.Bool("json", true, "JSON output")
	if err := f.Parse(args[1:]); err != nil {
		return 2, err
	}
	if len(f.Args()) > 0 {
		return 2, fmt.Errorf("unexpected positional arguments")
	}
	if args[0] == "list" {
		var s model.Status
		if err := a.call("status", nil, &s); err != nil {
			return 1, err
		}
		rows := []model.ResourceView{}
		for _, r := range s.Resources {
			if r.Kind == "port" {
				rows = append(rows, r)
			}
		}
		return 0, a.print(rows)
	}
	if args[0] == "discover" {
		var result any
		if err := a.call("ports_discover", nil, &result); err != nil {
			return 1, err
		}
		return 0, a.print(result)
	}
	return 2, fmt.Errorf("unknown ports action %q", args[0])
}
