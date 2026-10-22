//go:build darwin

package database

import "golang.org/x/sys/unix"

func statModified(s *unix.Stat_t) int64 { return s.Mtim.Sec*1000000000 + s.Mtim.Nsec }
func statChanged(s *unix.Stat_t) int64  { return s.Ctim.Sec*1000000000 + s.Ctim.Nsec }
