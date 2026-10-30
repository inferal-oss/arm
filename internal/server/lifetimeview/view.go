// Package lifetimeview exposes only original nonretiring borrower views.
package lifetimeview

import "getarm.org/internal/server/internal/nativeowner"

type ManagerView = nativeowner.ManagerView
type ServerView = nativeowner.ServerView
type ManagerClaim = nativeowner.ManagerClaim
type ServerClaim = nativeowner.ServerClaim
type ManagerStop = nativeowner.ManagerStop
type ServerStop = nativeowner.ServerStop
type Snapshot = nativeowner.Snapshot

// Deliberately no issuer, NativeClosure, constructor or retirement alias.
