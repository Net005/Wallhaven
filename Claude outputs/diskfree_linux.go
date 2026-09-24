//go:build linux

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

func diskUsage(p string) (free, total uint64) {
	for p != "/" && p != "." {
		if _, err := os.Stat(p); err == nil {
			break
		}
		p = filepath.Dir(p)
	}
	var st syscall.Statfs_t
	if syscall.Statfs(p, &st) == nil {
		return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize)
	}
	return 0, 0
}
