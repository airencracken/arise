//go:build !linux

package binpkg

import "time"

type SparseExtent struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

func readExtendedAttributes(string, bool) (map[string]string, error) { return nil, nil }
func applyExtendedAttributes(string, map[string]string, bool) error  { return nil }
func sparseMap(string, int64) ([]SparseExtent, error)                { return nil, nil }
func encodeSparseMap([]SparseExtent) (string, error)                 { return "", nil }
func setSymlinkTimes(string, time.Time, time.Time) error             { return nil }

const (
	xattrPAXPrefix = "ARISE.xattr."
	sparsePAXKey   = "ARISE.sparse.extents"
)
