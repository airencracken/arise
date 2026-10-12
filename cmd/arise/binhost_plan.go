package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/resolve"
)

type remoteBinaryArchive struct {
	host  string
	entry binpkg.PackageIndexEntry
}

func loadRemoteBinaryCandidates(ctx context.Context, cfg *resolve.ResolveConfig) (map[string]remoteBinaryArchive, error) {
	archives := make(map[string]remoteBinaryArchive)
	if !cfg.GetBinPkg && !cfg.GetBinPkgOnly {
		return archives, nil
	}
	if cfg.BinpkgDir == "" {
		cfg.BinpkgDir = "/var/cache/binpkgs"
	}
	var failures []error
	for _, host := range cfg.BinhostURLs {
		index, err := binpkg.FetchBinhostIndex(ctx, host)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", host, err))
			continue
		}
		for _, entry := range index.Packages {
			info, err := binpkg.PackageInfoFromIndex(entry, cfg.BinpkgDir)
			if err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", host, err))
				continue
			}
			if _, exists := archives[info.Path]; exists {
				continue
			}
			archives[info.Path] = remoteBinaryArchive{host: host, entry: entry}
			cfg.RemoteBinpkgs = append(cfg.RemoteBinpkgs, info)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(archives) == 0 && cfg.GetBinPkgOnly {
		return nil, fmt.Errorf("no usable remote binary metadata: %w", errors.Join(failures...))
	}
	return archives, nil
}

func acquirePlannedBinaries(ctx context.Context, archives map[string]remoteBinaryArchive, actions []resolve.PkgAction, destination string) error {
	for _, action := range actions {
		if action.MergeType != "binary" {
			continue
		}
		remote, exists := archives[action.BinaryPath]
		if !exists {
			continue
		}
		if _, err := binpkg.ReadInfo(action.BinaryPath); err == nil {
			continue
		}
		path, err := binpkg.AcquireIndexedPackage(ctx, remote.host, destination, remote.entry)
		if err != nil {
			return err
		}
		info, err := binpkg.ReadInfo(path)
		if err != nil {
			return err
		}
		if info.Subslot == "" {
			info.Subslot = info.Slot
		}
		advertised, err := binpkg.PackageInfoFromIndex(remote.entry, destination)
		if err != nil {
			return err
		}
		actualUse, advertisedUse := strings.Fields(info.Use), strings.Fields(advertised.Use)
		slices.Sort(actualUse)
		slices.Sort(advertisedUse)
		if action.Atom == nil || info.CPV() != action.Atom.CPV() || info.Slot != action.Slot ||
			(action.Subslot != "" && info.Subslot != action.Subslot) || info.Subslot != advertised.Subslot ||
			info.EAPI != advertised.EAPI || !slices.Equal(actualUse, advertisedUse) ||
			info.CHOST != advertised.CHOST || info.ABI != advertised.ABI || info.Repository != advertised.Repository ||
			(advertised.BuildID != "" && info.BuildID != advertised.BuildID) {
			return fmt.Errorf("acquired archive metadata differs from selected package %s", action.Atom)
		}
	}
	return nil
}
