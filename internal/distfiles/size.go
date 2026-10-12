package distfiles

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// DownloadSizer counts shared Manifest artifacts once across a package plan.
// An unsuccessful call does not consume any artifacts from the total.
type DownloadSizer struct{ seen map[string]Artifact }

func ManifestDownloadSize(repo, category, pkg, srcURI, distdir string, use map[string]bool) (int64, error) {
	return new(DownloadSizer).Size(repo, category, pkg, srcURI, distdir, use)
}

func (s *DownloadSizer) Size(repo, category, pkg, srcURI, distdir string, use map[string]bool) (int64, error) {
	return s.size(repo, category, pkg, srcURI, distdir, use, func(path string, artifact Artifact) bool {
		return Verify(path, artifact) == nil
	})
}

// EstimateSize uses cache metadata to estimate missing download bytes without
// reading archive contents. Acquisition must still Verify cached artifacts;
// same-size corruption may increase the eventual download beyond this estimate.
func (s *DownloadSizer) EstimateSize(repo, category, pkg, srcURI, distdir string, use map[string]bool) (int64, error) {
	return s.size(repo, category, pkg, srcURI, distdir, use, func(path string, artifact Artifact) bool {
		info, err := os.Lstat(path)
		return err == nil && info.Mode().IsRegular() && info.Size() == artifact.Size
	})
}

func (s *DownloadSizer) size(repo, category, pkg, srcURI, distdir string, use map[string]bool, cached func(string, Artifact) bool) (int64, error) {
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
		if distdir != "" && cached(filepath.Join(distdir, artifact.Name), artifact) {
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
