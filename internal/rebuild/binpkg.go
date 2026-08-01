package rebuild

import (
	"bytes"
	"compress/bzip2"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/merge"
	dsbzip2 "github.com/dsnet/compress/bzip2"
)

const maxBinaryEnvironmentBytes = 64 << 20

func publishBuiltGPKG(ctx context.Context, cfg *RebuildConfig, category, pf, image string, metadata map[string]string, environment []byte) (string, error) {
	if cfg.PackageDir == "" {
		return "", fmt.Errorf("rebuild: PKGDIR is required when building a binary package")
	}
	lock, err := lockPackageIndex(cfg.PackageDir)
	if err != nil {
		return "", err
	}
	defer unlockPackageIndex(lock)
	indexPath := filepath.Join(cfg.PackageDir, "Packages")
	index, err := readOrCreatePackageIndex(indexPath)
	if err != nil {
		return "", err
	}
	cpv := category + "/" + pf
	buildID := nextBuildID(index, cpv)
	buildIDText := strconv.FormatUint(buildID, 10)
	metadata = cloneMetadata(metadata)
	metadata["BUILD_ID"] = buildIDText
	encoded := make(map[string][]byte, len(metadata)+1)
	for name, value := range metadata {
		encoded[name] = []byte(strings.TrimSuffix(value, "\n") + "\n")
	}
	if len(environment) != 0 {
		var compressed bytes.Buffer
		writer, err := dsbzip2.NewWriter(&compressed, nil)
		if err != nil {
			return "", fmt.Errorf("rebuild: create binary environment compressor: %w", err)
		}
		if _, err := writer.Write(environment); err != nil {
			_ = writer.Close()
			return "", fmt.Errorf("rebuild: compress binary environment: %w", err)
		}
		if err := writer.Close(); err != nil {
			return "", fmt.Errorf("rebuild: finish binary environment: %w", err)
		}
		encoded["environment.bz2"] = compressed.Bytes()
	}
	parsed, err := atom.Parse(cpv)
	if err != nil || parsed.Version == nil {
		return "", fmt.Errorf("rebuild: invalid package identity %s", cpv)
	}
	destination := filepath.Join(cfg.PackageDir, category, parsed.Package, pf+"-"+buildIDText+".gpkg.tar")
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return "", fmt.Errorf("rebuild: create binary package directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".part-*")
	if err != nil {
		return "", fmt.Errorf("rebuild: create binary package temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return "", err
	}
	_ = os.Remove(temporaryPath)
	published := false
	defer func() {
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := binpkg.CreateGPKG(ctx, binpkg.GPKGCreateRequest{
		Path: temporaryPath, Basename: pf, ImageRoot: image,
		Metadata: encoded, ModTime: time.Unix(0, 0).UTC(),
	}); err != nil {
		return "", fmt.Errorf("rebuild: create GPKG: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return "", fmt.Errorf("rebuild: publish GPKG: %w", err)
	}
	published = true
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return "", fmt.Errorf("rebuild: open binary package directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return "", fmt.Errorf("rebuild: sync binary package directory: %w", syncErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("rebuild: close binary package directory: %w", closeErr)
	}
	info, err := binpkg.ReadInfo(destination)
	if err != nil {
		return "", fmt.Errorf("rebuild: inspect published GPKG: %w", err)
	}
	entry, err := binpkg.NewPackageIndexEntry(destination, cfg.PackageDir, info, encoded)
	if err != nil {
		return "", fmt.Errorf("rebuild: index published GPKG: %w", err)
	}
	index.Packages = append(index.Packages, entry)
	if err := binpkg.WritePackagesIndex(indexPath, index, time.Now().UTC()); err != nil {
		return "", fmt.Errorf("rebuild: publish Packages index: %w", err)
	}
	return destination, nil
}

func lockPackageIndex(packageDir string) (*os.File, error) {
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		return nil, fmt.Errorf("rebuild: create PKGDIR: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(packageDir, ".arise-packages.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("rebuild: open Packages lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("rebuild: lock Packages index: %w", err)
	}
	return lock, nil
}

func unlockPackageIndex(lock *os.File) {
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = lock.Close()
}

func readOrCreatePackageIndex(path string) (*binpkg.PackagesIndex, error) {
	index, err := binpkg.ReadPackagesIndex(path)
	if err == nil {
		return index, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return &binpkg.PackagesIndex{Header: map[string]string{"VERSION": "0"}}, nil
	}
	return nil, fmt.Errorf("rebuild: read Packages index: %w", err)
}

func nextBuildID(index *binpkg.PackagesIndex, cpv string) uint64 {
	var maximum uint64
	for _, entry := range index.Packages {
		if entry["CPV"] != cpv {
			continue
		}
		if value, err := strconv.ParseUint(entry["BUILD_ID"], 10, 64); err == nil && value > maximum {
			maximum = value
		}
	}
	return maximum + 1
}

func cloneMetadata(source map[string]string) map[string]string {
	result := make(map[string]string, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func InstallBinaryPackage(ctx context.Context, atomStr string, cfg *RebuildConfig) error {
	if cfg == nil || cfg.BinaryPackagePath == "" {
		return fmt.Errorf("rebuild: binary package path is required")
	}
	root, err := filepath.Abs(cfg.RootDir)
	if err != nil {
		return err
	}
	if filepath.Clean(root) == string(filepath.Separator) && !cfg.AllowLiveRoot {
		return fmt.Errorf("rebuild: refusing live ROOT binary merge without authorization")
	}
	requested, err := atom.Parse(atomStr)
	if err != nil || requested.Version == nil {
		return fmt.Errorf("rebuild: binary merge requires an exact atom")
	}
	info, err := binpkg.ReadInfo(cfg.BinaryPackagePath)
	if err != nil {
		return fmt.Errorf("rebuild: inspect binary package: %w", err)
	}
	if info.Category != requested.Category || info.Package != requested.Package ||
		info.Version != requested.Version.Raw {
		return fmt.Errorf("rebuild: binary package identity %s does not match %s", info.CPV(), atomStr)
	}
	if cfg.SelectedSlot != "" {
		selectedSlot := info.Slot
		if info.Subslot != "" {
			selectedSlot += "/" + info.Subslot
		}
		if selectedSlot != cfg.SelectedSlot && info.Slot != cfg.SelectedSlot {
			return fmt.Errorf("rebuild: binary package slot %s does not match selected slot %s", selectedSlot, cfg.SelectedSlot)
		}
	}
	gpkgPolicy := binpkg.GPKGPolicy{Extraction: binpkg.DefaultExtractionPolicy}
	if cfg.BinaryPackageRequireSignature {
		gpkgPolicy.RequireSignature = true
		gpkgPolicy.VerifyManifest = binpkg.GPGVManifestVerifier(cfg.BinaryPackageTrustedKeyring)
	}
	var metadataBytes map[string][]byte
	if binpkg.IsGPKG(cfg.BinaryPackagePath) {
		pkg, policyErr := binpkg.ReadGPKGWithPolicy(ctx, cfg.BinaryPackagePath, gpkgPolicy)
		if policyErr != nil {
			return fmt.Errorf("rebuild: verify binary package: %w", policyErr)
		}
		metadataBytes = pkg.Metadata
	} else {
		metadataBytes, err = binpkg.ReadMetadata(ctx, cfg.BinaryPackagePath)
	}
	if err != nil {
		return fmt.Errorf("rebuild: read binary package metadata: %w", err)
	}
	metadata := make(map[string]string)
	var environment []byte
	for name, value := range metadataBytes {
		switch name {
		case "CONTENTS", ".environment":
			continue
		case "environment.bz2":
			reader := bzip2.NewReader(bytes.NewReader(value))
			environment, err = io.ReadAll(io.LimitReader(reader, maxBinaryEnvironmentBytes+1))
			if err != nil {
				return fmt.Errorf("rebuild: decompress binary environment: %w", err)
			}
			if len(environment) > maxBinaryEnvironmentBytes {
				return fmt.Errorf("rebuild: binary environment exceeds limits")
			}
			continue
		}
		if filepath.Base(name) != name || strings.ContainsAny(name, `/\`+"\x00") {
			return fmt.Errorf("rebuild: unsafe binary metadata name %q", name)
		}
		metadata[name] = strings.TrimSuffix(string(value), "\n")
	}
	for _, required := range []string{"CATEGORY", "PF", "SLOT", "EAPI"} {
		if metadata[required] == "" {
			return fmt.Errorf("rebuild: binary package metadata omits %s", required)
		}
	}
	if err := ensureWorkDirectory(cfg.WorkDirBase); err != nil {
		return err
	}
	image, err := os.MkdirTemp(cfg.WorkDirBase, "binary-"+requested.Package+"-*")
	if err != nil {
		return fmt.Errorf("rebuild: create binary image: %w", err)
	}
	defer os.RemoveAll(image)
	cfg.fireStage("extract")
	if err := binpkg.ExtractWithGPKGPolicy(ctx, cfg.BinaryPackagePath, image, gpkgPolicy); err != nil {
		return fmt.Errorf("rebuild: extract binary package: %w", err)
	}
	if cfg.CommitLock != nil {
		cfg.CommitLock.Lock()
		defer cfg.CommitLock.Unlock()
	}
	cfg.fireStage("merge")
	return merge.Merge(ctx, image, merge.MergeConfig{
		RootDir: cfg.RootDir, VdbDir: cfg.VdbDir,
		Category: info.Category, Package: info.Package, Version: info.Version,
		JournalDir: cfg.JournalDir, AllowLiveRoot: cfg.AllowLiveRoot,
		AllowLiveReplacement: cfg.AllowLiveReplacement,
		VDBLockHeld:          cfg.VDBLockHeld, VDBMetadata: metadata, Environment: environment,
		OnStage: cfg.fireStage, OnProgress: cfg.fireProgress,
	})
}

func PreflightBinaryPackage(atomStr string, cfg *RebuildConfig) error {
	if cfg == nil || cfg.BinaryPackagePath == "" || !filepath.IsAbs(cfg.BinaryPackagePath) {
		return fmt.Errorf("rebuild: preflight binary package path must be absolute")
	}
	for label, path := range map[string]string{
		"ROOT": cfg.RootDir, "VDB": cfg.VdbDir, "work": cfg.WorkDirBase, "journal": cfg.JournalDir,
	} {
		if path == "" || !filepath.IsAbs(path) {
			return fmt.Errorf("rebuild: preflight %s path must be absolute", label)
		}
	}
	requested, err := atom.Parse(atomStr)
	if err != nil || requested.Version == nil {
		return fmt.Errorf("rebuild: preflight binary requires an exact atom")
	}
	info, err := binpkg.ReadInfo(cfg.BinaryPackagePath)
	if err != nil {
		return err
	}
	if info.CPV() != requested.Category+"/"+requested.Package+"-"+requested.Version.Raw {
		return fmt.Errorf("rebuild: preflight binary identity mismatch")
	}
	return ensureWorkDirectory(cfg.WorkDirBase)
}
