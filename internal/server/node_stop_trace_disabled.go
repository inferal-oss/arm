//go:build !arm_devtrace

package server

import (
	"context"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
)

func traceStopPublished(*Server)                                                        {}
func traceStopServeEnd(*Server, error)                                                  {}
func traceStopReadRows(string, nodestop.Queryer) any                                    { return nil }
func traceStopObserved(string, NodeStopObservation, any)                                {}
func traceStopWorkerPause(*Server, context.Context) bool                                { return false }
func traceRestartReview(*NodeRestartReview, string, error)                              {}
func traceRestartCAS(string, nodestop.State, nodestop.Queryer, *nodestop.ElapsedReview) {}

func traceStopTeardown(*Server, error)                                                     {}
func traceStopMarker(string, nodestop.Record, nodestop.Closed, model.FileIdentity, string) {}
func traceStopGrant(string, mesh.Peer, mesh.Identity)                                      {}

func traceStopStartupRefused(string, nodestop.State) {}
