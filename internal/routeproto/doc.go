// Package routeproto contains an isolated feasibility checkpoint for approved
// ARM-only message routing. Its transport, TLS policy and closed probe handler
// exist only in tests: no production package imports this package, no daemon
// handler is registered, and no membership or message ledger is implemented.
package routeproto
