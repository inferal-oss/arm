//go:build linux

package storage

func sourceMountFD(fd int) (string, error) { return mountIdentity(fd) }
