package distfiles

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// DownloadSizer counts shared Manifest artifacts once across a package plan.
// Cache eligibility uses the same size and digest checks as acquisition.
// An unsuccessful call does not consume any artifacts from the total.
type DownloadSizer struct{ seen map[string]Artifact }

func ManifestDownloadSize(repo, category, pkg, srcURI, distdir string, use map[string]bool) (int64, error) {
	return new(DownloadSizer).Size(repo, category, pkg, srcURI, distdir, use)
}

func (s *DownloadSizer) Size(repo, category, pkg, srcURI, distdir string, use map[string]bool) (int64, error) {
	if repo == "" || srcURI == "" {
		return 0, nil
	}
	file, err := os.Open(filepath.Join(repo, category, pkg, "Manifest"))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer file.Close()
	artifacts, err := Plan(file, srcURI, use)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, artifact := range artifacts {
		if previous, ok := s.seen[artifact.Name]; ok {
			if !sameIdentity(previous, artifact) {
				return 0, fmt.Errorf("conflicting download identity for %s", artifact.Name)
			}
			continue
		}
		if distdir != "" && Verify(filepath.Join(distdir, artifact.Name), artifact) == nil {
			continue
		}
		if artifact.Size > math.MaxInt64-total {
			return 0, fmt.Errorf("download size exceeds int64")
		}
		total += artifact.Size
	}
	if s.seen == nil {
		s.seen = make(map[string]Artifact)
	}
	for _, artifact := range artifacts {
		s.seen[artifact.Name] = artifact
	}
	return total, nil
}
