package recoveryset

import (
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/merge"
	"github.com/airencracken/arise/internal/oplock"
)

type RestorePlan struct {
	Schema               int                     `json:"schema"`
	SetID                string                  `json:"set_id"`
	OperationID          string                  `json:"operation_id"`
	Artifacts            []Artifact              `json:"artifacts"`
	Drift                []string                `json:"drift,omitempty"`
	DriftApprovalSHA256  string                  `json:"drift_approval_sha256,omitempty"`
	CurrentConfiguration binpkg.InputFingerprint `json:"current_configuration"`
	CurrentRepository    binpkg.InputFingerprint `json:"current_repository"`
}

type RestoreRequest struct {
	SetPath              string
	RootDir              string
	VDBDir               string
	JournalDir           string
	WorkDir              string
	CurrentConfiguration binpkg.InputFingerprint
	CurrentRepository    binpkg.InputFingerprint
	ApprovedDriftSHA256  string
	AllowLiveRoot        bool
	Merge                func(context.Context, string, merge.MergeConfig) error
}

func InspectRestore(request RestoreRequest) (*RestorePlan, error) {
	manifest, err := Read(request.SetPath)
	if err != nil {
		return nil, err
	}
	_, err = ReadStatus(request.SetPath)
	if err != nil {
		return nil, err
	}
	artifacts := append([]Artifact(nil), manifest.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].CaptureOrder > artifacts[j].CaptureOrder
	})
	plan := &RestorePlan{
		Schema: 1, SetID: manifest.SetID, OperationID: manifest.OperationID,
		Artifacts: artifacts, CurrentConfiguration: request.CurrentConfiguration,
		CurrentRepository: request.CurrentRepository,
	}
	if manifest.Capture.ConfigurationFingerprint != request.CurrentConfiguration {
		plan.Drift = append(plan.Drift, "Portage configuration fingerprint differs from capture")
	}
	if manifest.Capture.RepositoryFingerprint != request.CurrentRepository {
		plan.Drift = append(plan.Drift, "repository fingerprint differs from capture")
	}
	if len(plan.Drift) > 0 {
		plan.DriftApprovalSHA256, err = restoreApprovalSHA256(*plan)
		if err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func Restore(ctx context.Context, request RestoreRequest) error {
	if request.SetPath == "" || request.RootDir == "" || request.VDBDir == "" || request.JournalDir == "" {
		return fmt.Errorf("recovery set: restore requires set, ROOT, VDB and journal paths")
	}
	plan, err := InspectRestore(request)
	if err != nil {
		return err
	}
	if len(plan.Drift) > 0 && request.ApprovedDriftSHA256 != plan.DriftApprovalSHA256 {
		return fmt.Errorf("recovery set: restore drift requires approval digest %s", plan.DriftApprovalSHA256)
	}
	if len(plan.Drift) == 0 && request.ApprovedDriftSHA256 != "" {
		return fmt.Errorf("recovery set: drift approval was supplied but current inputs match")
	}
	workDir := request.WorkDir
	if workDir == "" {
		workDir = filepath.Join(filepath.Dir(request.SetPath), ".restore-work")
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("recovery set: create restore work directory: %w", err)
	}
	if err := MarkStatus(request.SetPath, StatusPendingRollback, ""); err != nil {
		return err
	}
	lock, err := oplock.TryAcquireVDB(request.VDBDir)
	if err != nil {
		return fmt.Errorf("recovery set: acquire restore VDB lock: %w", err)
	}
	defer lock.Release()
	mergePackage := request.Merge
	if mergePackage == nil {
		mergePackage = merge.Merge
	}
	for _, artifact := range plan.Artifacts {
		if err := ctx.Err(); err != nil {
			return err
		}
		artifactPath := filepath.Join(request.SetPath, filepath.FromSlash(artifact.Path))
		recoveryManifest, err := binpkg.ReadRecoveryManifest(artifactPath)
		if err != nil {
			return err
		}
		image, err := os.MkdirTemp(workDir, "image-*")
		if err != nil {
			return fmt.Errorf("recovery set: create restore image: %w", err)
		}
		restoreErr := func() error {
			defer os.RemoveAll(image)
			if err := binpkg.Extract(ctx, artifactPath, image); err != nil {
				return fmt.Errorf("recovery set: extract %s: %w", artifact.CPV, err)
			}
			metadata, environment, err := restoreVDBMetadata(recoveryManifest)
			if err != nil {
				return err
			}
			targetVDB := filepath.Join(request.VDBDir, recoveryManifest.Package.Category,
				recoveryManifest.Package.Package+"-"+recoveryManifest.Package.Version)
			cfg := merge.MergeConfig{
				RootDir: request.RootDir, VdbDir: request.VDBDir,
				Category: recoveryManifest.Package.Category, Package: recoveryManifest.Package.Package,
				Version: recoveryManifest.Package.Version, JournalDir: request.JournalDir,
				AllowLiveRoot: request.AllowLiveRoot, AllowLiveReplacement: true,
				VDBLockHeld: true, ReplacedVDBPath: targetVDB,
				VDBMetadata: metadata, Environment: environment,
			}
			if err := mergePackage(ctx, image, cfg); err != nil {
				return fmt.Errorf("recovery set: restore %s: %w", artifact.CPV, err)
			}
			return nil
		}()
		if restoreErr != nil {
			return restoreErr
		}
	}
	if err := MarkStatus(request.SetPath, StatusPendingVerification, ""); err != nil {
		return fmt.Errorf("recovery set: packages restored but status update failed: %w", err)
	}
	return nil
}

func restoreVDBMetadata(manifest *binpkg.RecoveryManifest) (map[string]string, []byte, error) {
	metadata := make(map[string]string)
	var environment []byte
	for _, item := range manifest.SourceVDB {
		if item.Type != "file" {
			continue
		}
		content, err := base64.StdEncoding.DecodeString(item.ContentBase64)
		if err != nil {
			return nil, nil, fmt.Errorf("recovery set: decode VDB file %s: %w", item.Path, err)
		}
		switch item.Path {
		case "CONTENTS", ".environment":
			continue
		case "environment.bz2":
			reader := bzip2.NewReader(strings.NewReader(string(content)))
			environment, err = io.ReadAll(reader)
			if err != nil {
				return nil, nil, fmt.Errorf("recovery set: decompress saved environment: %w", err)
			}
			continue
		}
		if filepath.Base(item.Path) != item.Path || strings.ContainsAny(item.Path, `/\`+"\x00") {
			return nil, nil, fmt.Errorf("recovery set: nested VDB file %q cannot be restored transactionally", item.Path)
		}
		metadata[item.Path] = strings.TrimSuffix(string(content), "\n")
	}
	for _, required := range []string{"CATEGORY", "PF", "SLOT", "EAPI"} {
		if metadata[required] == "" {
			return nil, nil, fmt.Errorf("recovery set: saved VDB omits required %s", required)
		}
	}
	return metadata, environment, nil
}

func restoreApprovalSHA256(plan RestorePlan) (string, error) {
	document := struct {
		Schema               int                     `json:"schema"`
		SetID                string                  `json:"set_id"`
		OperationID          string                  `json:"operation_id"`
		Artifacts            []Artifact              `json:"artifacts"`
		Drift                []string                `json:"drift"`
		CurrentConfiguration binpkg.InputFingerprint `json:"current_configuration"`
		CurrentRepository    binpkg.InputFingerprint `json:"current_repository"`
	}{
		Schema: plan.Schema, SetID: plan.SetID, OperationID: plan.OperationID,
		Artifacts: plan.Artifacts, Drift: plan.Drift,
		CurrentConfiguration: plan.CurrentConfiguration, CurrentRepository: plan.CurrentRepository,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("recovery set: encode restore approval: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
