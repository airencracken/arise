//go:build !linux

package merge

func copyXattrs(source, target string, noFollow bool) error { return nil }
