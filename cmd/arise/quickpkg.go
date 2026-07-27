package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/binpkg"
)

func runQuickPkg(args []string) {
	gpkgFormat := false
	if len(args) > 0 && args[0] == "--gpkg" {
		gpkgFormat = true
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "quickpkg: missing package atom argument\n")
		fmt.Fprintf(os.Stderr, "Usage: arise quickpkg [--gpkg] <atom>\n")
		os.Exit(1)
	}

	a, err := atom.Parse(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "quickpkg: parsing atom %q: %v\n", args[0], err)
		os.Exit(1)
	}

	vdbPath := filepath.Join(*vdbDir, a.Category, a.Package)
	if a.Version != nil && a.Version.Raw != "" {
		vdbPath = vdbPath + "-" + a.Version.Raw
	}

	ctx := context.Background()
	if gpkgFormat {
		outPath, err := binpkg.CreateInstalledGPKG(ctx, vdbPath, commandEnv("ROOT", "/"), *binpkgDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "quickpkg: %v\n", err)
			os.Exit(1)
		}
		if _, err := binpkg.ReadGPKG(ctx, outPath); err != nil {
			fmt.Fprintf(os.Stderr, "quickpkg: verify GPKG: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "quickpkg: created GLEP 78 GPKG artifact")
		fmt.Println(outPath)
		return
	}
	configurationFingerprint, err := binpkg.FingerprintConfiguration(*portageConfigRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "quickpkg: fingerprint Portage configuration: %v\n", err)
		os.Exit(1)
	}
	repositoryFingerprint, err := binpkg.FingerprintRepositoryIdentity(*repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "quickpkg: fingerprint repository identity: %v\n", err)
		os.Exit(1)
	}
	outPath, err := binpkg.CreateRecoveryArtifact(ctx, binpkg.CaptureRequest{
		VDBEntryPath: vdbPath,
		RootDir:      commandEnv("ROOT", "/"),
		PackageDir:   *binpkgDir,
		Provenance: binpkg.CaptureProvenance{
			Schema:                   binpkg.CaptureContextSchema,
			OperationKind:            "manual-quickpkg",
			ConfigurationFingerprint: configurationFingerprint,
			RepositoryFingerprint:    repositoryFingerprint,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "quickpkg: %v\n", err)
		os.Exit(1)
	}

	manifest, err := binpkg.ReadRecoveryManifest(outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "quickpkg: verify recovery provenance: %v\n", err)
		os.Exit(1)
	}
	notice, err := recoveryArtifactNotice(manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "quickpkg: verify recovery provenance: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, notice)
	fmt.Println(outPath)
}

func recoveryArtifactNotice(manifest *binpkg.RecoveryManifest) (string, error) {
	digest, err := manifest.SHA256()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("quickpkg: created %s artifact schema=%d manifest-sha256=%s", manifest.ArtifactKind, manifest.Schema, digest), nil
}
