package binpkg

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const (
	RecoveryManifestSchema = 1
	ArtifactKindRecovery   = "host-recovery"

	recoveryManifestKey       = "ARISE_RECOVERY_MANIFEST_B64"
	recoveryManifestSHA256Key = "ARISE_RECOVERY_MANIFEST_SHA256"
)

type RecoveryManifest struct {
	Schema           int               `json:"schema"`
	ArtifactKind     string            `json:"artifact_kind"`
	Package          PackageIdentity   `json:"package"`
	Capture          CaptureProvenance `json:"capture"`
	SourceVDB        []FileEvidence    `json:"source_vdb"`
	Payload          []FileEvidence    `json:"payload"`
	SourceVDBSHA256  string            `json:"source_vdb_sha256"`
	SourceRootSHA256 string            `json:"source_root_sha256"`
}

type PackageIdentity struct {
	Category   string `json:"category"`
	Package    string `json:"package"`
	Version    string `json:"version"`
	Slot       string `json:"slot"`
	Subslot    string `json:"subslot,omitempty"`
	Repository string `json:"repository"`
	EAPI       string `json:"eapi"`
	Use        string `json:"use"`
	BuildID    string `json:"build_id,omitempty"`
	ABI        string `json:"abi,omitempty"`
	CBuild     string `json:"cbuild,omitempty"`
	CHOST      string `json:"chost,omitempty"`
	CTarget    string `json:"ctarget,omitempty"`
	BuildTime  int64  `json:"build_time"`
}

type FileEvidence struct {
	Path          string            `json:"path"`
	Type          string            `json:"type"`
	Mode          uint32            `json:"mode"`
	UID           uint32            `json:"uid"`
	GID           uint32            `json:"gid"`
	Size          int64             `json:"size"`
	MtimeUnixNano int64             `json:"mtime_unix_nano"`
	SHA256        string            `json:"sha256,omitempty"`
	LinkTarget    string            `json:"link_target,omitempty"`
	ContentBase64 string            `json:"content_base64,omitempty"`
	RecordedType  string            `json:"recorded_type,omitempty"`
	XAttrs        map[string]string `json:"xattrs,omitempty"`
	SparseExtents []SparseExtent    `json:"sparse_extents,omitempty"`
}

func (manifest *RecoveryManifest) SHA256() (string, error) {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("binpkg: encode recovery manifest digest: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func buildRecoveryManifest(meta map[string]string, vdbEntryPath, rootDir string, entries []contentEntry, provenance CaptureProvenance) (*RecoveryManifest, []byte, error) {
	slot, subslot := parseSlot(meta["SLOT"])
	buildTime, err := strconv.ParseInt(meta["BUILD_TIME"], 10, 64)
	if err != nil && meta["BUILD_TIME"] != "" {
		return nil, nil, fmt.Errorf("binpkg: invalid BUILD_TIME %q: %w", meta["BUILD_TIME"], err)
	}
	manifest := &RecoveryManifest{
		Schema:       RecoveryManifestSchema,
		ArtifactKind: ArtifactKindRecovery,
		Capture:      provenance,
		Package: PackageIdentity{
			Category: meta["CATEGORY"], Package: meta["PACKAGE"], Version: meta["VERSION"],
			Slot: slot, Subslot: subslot, Repository: meta["repository"], EAPI: meta["EAPI"],
			Use: meta["USE"], BuildID: meta["BUILD_ID"], ABI: meta["ABI"], CBuild: meta["CBUILD"],
			CHOST: meta["CHOST"], CTarget: meta["CTARGET"], BuildTime: buildTime,
		},
	}

	manifest.SourceVDB, err = captureVDBEvidence(vdbEntryPath)
	if err != nil {
		return nil, nil, err
	}
	manifest.Payload, err = capturePayloadEvidence(rootDir, entries)
	if err != nil {
		return nil, nil, err
	}
	manifest.SourceVDBSHA256, err = evidenceSHA256(manifest.SourceVDB)
	if err != nil {
		return nil, nil, err
	}
	manifest.SourceRootSHA256, err = evidenceSHA256(manifest.Payload)
	if err != nil {
		return nil, nil, err
	}
	if err := validateRecoveryManifest(manifest); err != nil {
		return nil, nil, fmt.Errorf("binpkg: validate captured recovery manifest: %w", err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, nil, fmt.Errorf("binpkg: encode recovery manifest: %w", err)
	}
	return manifest, encoded, nil
}

func captureVDBEvidence(root string) ([]FileEvidence, error) {
	var evidence []FileEvidence
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return fmt.Errorf("binpkg: VDB path %s escapes its entry root", path)
		}
		item, err := fileEvidence(path, filepath.ToSlash(relative), "")
		if err != nil {
			return fmt.Errorf("binpkg: capture VDB evidence for %s: %w", relative, err)
		}
		if item.Type == "file" {
			content, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("binpkg: read VDB file %s: %w", relative, err)
			}
			item.ContentBase64 = base64.StdEncoding.EncodeToString(content)
			item.Size = int64(len(content))
			sum := sha256.Sum256(content)
			item.SHA256 = hex.EncodeToString(sum[:])
		}
		evidence = append(evidence, item)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("binpkg: capture complete VDB entry: %w", err)
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].Path < evidence[j].Path })
	return evidence, nil
}

func capturePayloadEvidence(root string, entries []contentEntry) ([]FileEvidence, error) {
	evidence := make([]FileEvidence, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	type inodeKey struct {
		dev uint64
		ino uint64
	}
	hardlinks := make(map[inodeKey]string)
	for _, entry := range entries {
		archivePath, sourcePath, err := installedContentPath(root, entry.Path)
		if err != nil {
			return nil, fmt.Errorf("binpkg: invalid installed path %q: %w", entry.Path, err)
		}
		if _, exists := seen[archivePath]; exists {
			return nil, fmt.Errorf("binpkg: duplicate installed path %q", entry.Path)
		}
		seen[archivePath] = struct{}{}
		if err := rejectSymlinkParents(root, sourcePath); err != nil {
			return nil, fmt.Errorf("binpkg: cannot capture installed path %s: %w", entry.Path, err)
		}
		item, err := fileEvidence(sourcePath, archivePath, entry.Type)
		if err != nil {
			return nil, fmt.Errorf("binpkg: capture installed path %s: %w", entry.Path, err)
		}
		switch entry.Type {
		case "obj":
			if item.Type != "file" {
				return nil, fmt.Errorf("binpkg: installed path %s changed type: CONTENTS records a regular file, found %s", entry.Path, item.Type)
			}
			info, statErr := os.Lstat(sourcePath)
			if statErr != nil {
				return nil, fmt.Errorf("binpkg: inspect hardlink identity for %s: %w", entry.Path, statErr)
			}
			if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
				key := inodeKey{dev: uint64(stat.Dev), ino: stat.Ino}
				if first, exists := hardlinks[key]; exists {
					item.Type = "hardlink"
					item.LinkTarget = first
				} else {
					hardlinks[key] = archivePath
				}
			}
		case "dir":
			if item.Type != "directory" {
				return nil, fmt.Errorf("binpkg: installed path %s changed type: CONTENTS records a directory, found %s", entry.Path, item.Type)
			}
		case "sym":
			if item.Type != "symlink" {
				return nil, fmt.Errorf("binpkg: installed path %s changed type: CONTENTS records a symlink, found %s", entry.Path, item.Type)
			}
			if item.LinkTarget != entry.Target {
				return nil, fmt.Errorf("binpkg: installed symlink %s changed target: CONTENTS records %q, found %q", entry.Path, entry.Target, item.LinkTarget)
			}
		default:
			return nil, fmt.Errorf("binpkg: unsupported CONTENTS entry type %q for %s", entry.Type, entry.Path)
		}
		evidence = append(evidence, item)
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].Path < evidence[j].Path })
	return evidence, nil
}

func fileEvidence(sourcePath, recordedPath, recordedType string) (FileEvidence, error) {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return FileEvidence{}, err
	}
	item := FileEvidence{
		Path: recordedPath, Mode: uint32(info.Mode()), Size: info.Size(),
		MtimeUnixNano: info.ModTime().UnixNano(), RecordedType: recordedType,
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		item.UID = stat.Uid
		item.GID = stat.Gid
	}
	item.XAttrs, err = readExtendedAttributes(sourcePath, info.Mode()&os.ModeSymlink != 0)
	if err != nil {
		return FileEvidence{}, err
	}
	switch {
	case info.Mode().IsRegular():
		item.Type = "file"
		file, err := os.Open(sourcePath)
		if err != nil {
			return FileEvidence{}, err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return FileEvidence{}, copyErr
		}
		if closeErr != nil {
			return FileEvidence{}, closeErr
		}
		item.SHA256 = hex.EncodeToString(hash.Sum(nil))
		item.SparseExtents, err = sparseMap(sourcePath, info.Size())
		if err != nil {
			return FileEvidence{}, err
		}
	case info.IsDir():
		item.Type = "directory"
	case info.Mode()&os.ModeSymlink != 0:
		item.Type = "symlink"
		item.LinkTarget, err = os.Readlink(sourcePath)
		if err != nil {
			return FileEvidence{}, err
		}
	default:
		return FileEvidence{}, fmt.Errorf("unsupported filesystem type %s", info.Mode().Type())
	}
	return item, nil
}

func evidenceSHA256(evidence []FileEvidence) (string, error) {
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return "", fmt.Errorf("binpkg: encode file evidence: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func embedRecoveryManifest(meta map[string]string, encoded []byte) {
	sum := sha256.Sum256(encoded)
	meta[recoveryManifestKey] = base64.StdEncoding.EncodeToString(encoded)
	meta[recoveryManifestSHA256Key] = hex.EncodeToString(sum[:])
}

func ReadRecoveryManifest(path string) (*RecoveryManifest, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("binpkg: read recovery manifest for %s: %w", path, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("binpkg: open recovery artifact %s: %w", path, err)
	}
	defer file.Close()
	meta, err := readXPAKMetadata(file, fileInfo.Size())
	if err != nil {
		return nil, err
	}
	raw, ok := meta[recoveryManifestKey]
	if !ok {
		return nil, fmt.Errorf("binpkg: package is not an Arise recovery artifact")
	}
	encoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("binpkg: invalid recovery manifest encoding: %w", err)
	}
	sum := sha256.Sum256(encoded)
	if meta[recoveryManifestSHA256Key] != hex.EncodeToString(sum[:]) {
		return nil, fmt.Errorf("binpkg: recovery manifest digest mismatch")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var manifest RecoveryManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("binpkg: invalid recovery manifest schema: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if err := validateRecoveryManifest(&manifest); err != nil {
		return nil, err
	}
	slot, subslot := parseSlot(meta["SLOT"])
	buildTime, buildTimeErr := strconv.ParseInt(meta["BUILD_TIME"], 10, 64)
	if buildTimeErr != nil && meta["BUILD_TIME"] != "" {
		return nil, fmt.Errorf("binpkg: invalid package BUILD_TIME: %w", buildTimeErr)
	}
	if manifest.Package.Category != meta["CATEGORY"] ||
		manifest.Package.Package != meta["PACKAGE"] ||
		manifest.Package.Version != meta["VERSION"] ||
		manifest.Package.Slot != slot ||
		manifest.Package.Subslot != subslot ||
		manifest.Package.Repository != meta["repository"] ||
		manifest.Package.EAPI != meta["EAPI"] ||
		manifest.Package.Use != meta["USE"] ||
		manifest.Package.BuildID != meta["BUILD_ID"] ||
		manifest.Package.ABI != meta["ABI"] ||
		manifest.Package.CBuild != meta["CBUILD"] ||
		manifest.Package.CHOST != meta["CHOST"] ||
		manifest.Package.CTarget != meta["CTARGET"] ||
		manifest.Package.BuildTime != buildTime {
		return nil, fmt.Errorf("binpkg: recovery manifest package identity disagrees with package metadata")
	}
	return &manifest, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("binpkg: recovery manifest contains trailing JSON")
		}
		return fmt.Errorf("binpkg: invalid recovery manifest trailer: %w", err)
	}
	return nil
}

func validateRecoveryManifest(manifest *RecoveryManifest) error {
	if manifest.Schema != RecoveryManifestSchema {
		return fmt.Errorf("binpkg: unsupported recovery manifest schema %d", manifest.Schema)
	}
	if manifest.ArtifactKind != ArtifactKindRecovery {
		return fmt.Errorf("binpkg: unsupported artifact kind %q", manifest.ArtifactKind)
	}
	if err := validateCaptureProvenance(manifest.Capture); err != nil {
		return fmt.Errorf("binpkg: invalid recovery manifest capture context: %w", err)
	}
	for _, field := range []struct{ name, value string }{
		{"category", manifest.Package.Category},
		{"package", manifest.Package.Package},
		{"version", manifest.Package.Version},
	} {
		if err := validatePackagePathComponent(field.value); err != nil {
			return fmt.Errorf("binpkg: invalid recovery manifest package %s: %w", field.name, err)
		}
	}
	vdbDigest, err := evidenceSHA256(manifest.SourceVDB)
	if err != nil || vdbDigest != manifest.SourceVDBSHA256 {
		return fmt.Errorf("binpkg: recovery manifest VDB evidence digest mismatch")
	}
	rootDigest, err := evidenceSHA256(manifest.Payload)
	if err != nil || rootDigest != manifest.SourceRootSHA256 {
		return fmt.Errorf("binpkg: recovery manifest payload evidence digest mismatch")
	}
	if err := validateEvidencePaths(manifest.SourceVDB, true); err != nil {
		return fmt.Errorf("binpkg: invalid recovery manifest VDB evidence: %w", err)
	}
	if err := validateEvidencePaths(manifest.Payload, false); err != nil {
		return fmt.Errorf("binpkg: invalid recovery manifest payload evidence: %w", err)
	}
	return nil
}

func validateEvidencePaths(evidence []FileEvidence, requireFileContent bool) error {
	previous := ""
	for _, item := range evidence {
		if item.Path == "" || filepath.IsAbs(item.Path) {
			return fmt.Errorf("path %q is not relative", item.Path)
		}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(item.Path)))
		if clean != item.Path || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("path %q is not canonical", item.Path)
		}
		if previous != "" && item.Path <= previous {
			return fmt.Errorf("paths are duplicate or unsorted at %q", item.Path)
		}
		previous = item.Path
		switch item.Type {
		case "file", "hardlink":
			decodedDigest, err := hex.DecodeString(item.SHA256)
			if err != nil || len(decodedDigest) != sha256.Size {
				return fmt.Errorf("file %q has an invalid digest", item.Path)
			}
			if item.Type == "hardlink" {
				if item.LinkTarget == "" || requireFileContent {
					return fmt.Errorf("hardlink %q has invalid evidence", item.Path)
				}
			} else if requireFileContent {
				content, err := base64.StdEncoding.DecodeString(item.ContentBase64)
				if err != nil {
					return fmt.Errorf("file %q has invalid VDB content encoding", item.Path)
				}
				sum := sha256.Sum256(content)
				if item.Size != int64(len(content)) || item.SHA256 != hex.EncodeToString(sum[:]) {
					return fmt.Errorf("file %q content disagrees with its evidence", item.Path)
				}
			} else if item.ContentBase64 != "" {
				return fmt.Errorf("payload file %q unexpectedly embeds content", item.Path)
			}
		case "directory":
		case "symlink":
			if item.LinkTarget == "" {
				return fmt.Errorf("symlink %q has no target", item.Path)
			}
		default:
			return fmt.Errorf("path %q has unsupported type %q", item.Path, item.Type)
		}
	}
	return nil
}
