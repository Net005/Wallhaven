//go:build !linux

package main

func diskUsage(p string) (free, total uint64) { return 0, 0 }
