package recoveryset

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
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
	"time"

	"github.com/airencracken/arise/internal/binpkg"
)

const Schema = 1

type Package struct {
	VDBEntryPath string
}

type Request struct {
	Directory                string
	SetID                    string
	OperationID              string
	PlanSHA256               string
	RootDir                  string
	Packages                 []Package
	ConfigurationFingerprint binpkg.InputFingerprint
	RepositoryFingerprint    binpkg.InputFingerprint
	Capture                  func(context.Context, binpkg.CaptureRequest) (string, error)
}

type Manifest struct {
	Schema             int                      `json:"schema"`
	State              string                   `json:"state"`
	SetID              string                   `json:"set_id"`
	OperationID        string                   `json:"operation_id"`
	PlanSHA256         string                   `json:"plan_sha256"`
	Capture            binpkg.CaptureProvenance `json:"capture"`
	Artifacts          []Artifact               `json:"artifacts"`
	SignatureAlgorithm string                   `json:"signature_algorithm"`
	SignerPublicKey    string                   `json:"signer_public_key"`
	Signature          string                   `json:"signature"`
}

type Artifact struct {
	Identity       string `json:"identity"`
	CaptureOrder   int    `json:"capture_order"`
	CPV            string `json:"cpv"`
	Slot           string `json:"slot"`
	Subslot        string `json:"subslot,omitempty"`
	Repository     string `json:"repository"`
	BuildID        string `json:"build_id,omitempty"`
	Path           string `json:"path"`
	SHA256         string `json:"sha256"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

func NewID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("recovery set: generate ID: %w", err)
	}
	return hex.EncodeToString(random), nil
}

func Publish(ctx context.Context, request Request) (string, error) {
	if err := validateRequest(request); err != nil {
		return "", err
	}
	capture := request.Capture
	if capture == nil {
		capture = binpkg.CreateRecoveryArtifact
	}
	setsDir := filepath.Join(request.Directory, "sets")
	if err := os.MkdirAll(setsDir, 0755); err != nil {
		return "", fmt.Errorf("recovery set: create sets directory: %w", err)
	}
	finalPath := filepath.Join(setsDir, request.SetID)
	if _, err := os.Lstat(finalPath); err == nil {
		return "", fmt.Errorf("recovery set: set %s already exists", request.SetID)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("recovery set: inspect destination: %w", err)
	}
	staging, err := os.MkdirTemp(setsDir, "."+request.SetID+".tmp-")
	if err != nil {
		return "", fmt.Errorf("recovery set: create staging directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(staging)
		}
	}()

	provenance := requestProvenance(request)
	objectProvenance := recoveryObjectProvenance(request)
	manifest := Manifest{
		Schema: Schema, State: "complete", SetID: request.SetID,
		OperationID: request.OperationID, PlanSHA256: request.PlanSHA256, Capture: provenance,
	}
	for captureOrder, pkg := range request.Packages {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("recovery set: capture cancelled: %w", err)
		}
		path, err := capture(ctx, binpkg.CaptureRequest{
			VDBEntryPath: pkg.VDBEntryPath,
			RootDir:      request.RootDir,
			PackageDir:   filepath.Join(staging, "artifacts"),
			Provenance:   objectProvenance,
		})
		if err != nil {
			return "", fmt.Errorf("recovery set: capture %s: %w", pkg.VDBEntryPath, err)
		}
		artifact, err := verifyCapturedArtifact(staging, path, request)
		if err != nil {
			return "", err
		}
		if err := materializeObject(request.Directory, path, artifact.SHA256); err != nil {
			return "", err
		}
		artifact.CaptureOrder = captureOrder
		manifest.Artifacts = append(manifest.Artifacts, artifact)
	}
	sort.Slice(manifest.Artifacts, func(i, j int) bool { return manifest.Artifacts[i].Identity < manifest.Artifacts[j].Identity })
	privateKey, publicKey, err := loadOrCreateStoreSigner(request.Directory)
	if err != nil {
		return "", err
	}
	manifest.SignatureAlgorithm = "Ed25519"
	manifest.SignerPublicKey = base64.StdEncoding.EncodeToString(publicKey)
	signingBytes, err := manifestSigningBytes(manifest)
	if err != nil {
		return "", err
	}
	manifest.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, signingBytes))
	if err := validateManifest(manifest, len(request.Packages)); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("recovery set: encode manifest: %w", err)
	}
	if err := writeDurableFile(filepath.Join(staging, "manifest.json"), append(encoded, '\n')); err != nil {
		return "", err
	}
	captureBootID, err := CurrentBootID()
	if err != nil {
		return "", err
	}
	statusJSON, err := json.Marshal(Status{
		Schema: StatusSchema, SetID: request.SetID, Status: StatusActive, UpdatedAt: time.Now().UTC(),
		CaptureBootID: captureBootID,
	})
	if err != nil {
		return "", fmt.Errorf("recovery set: encode initial status: %w", err)
	}
	if err := writeDurableFile(filepath.Join(staging, "status.json"), append(statusJSON, '\n')); err != nil {
		return "", err
	}
	if err := syncDirectories(staging); err != nil {
		return "", err
	}
	if err := os.Rename(staging, finalPath); err != nil {
		return "", fmt.Errorf("recovery set: atomically publish set: %w", err)
	}
	published = true
	if err := syncDirectory(setsDir); err != nil {
		return "", fmt.Errorf("recovery set: sync published sets directory: %w", err)
	}
	return finalPath, nil
}

func verifyCapturedArtifact(staging, path string, request Request) (Artifact, error) {
	relative, err := filepath.Rel(staging, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return Artifact{}, fmt.Errorf("recovery set: capture returned an artifact outside staging")
	}
	recoveryManifest, err := binpkg.ReadRecoveryManifest(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("recovery set: verify artifact manifest: %w", err)
	}
	if recoveryManifest.Capture != recoveryObjectProvenance(request) {
		return Artifact{}, fmt.Errorf("recovery set: artifact provenance does not match its set")
	}
	artifactDigest, err := fileSHA256(path)
	if err != nil {
		return Artifact{}, err
	}
	manifestDigest, err := recoveryManifest.SHA256()
	if err != nil {
		return Artifact{}, err
	}
	cpv := recoveryManifest.Package.Category + "/" + recoveryManifest.Package.Package + "-" + recoveryManifest.Package.Version
	identity := cpv + ":" + recoveryManifest.Package.Slot + "/" + recoveryManifest.Package.Subslot +
		"::" + recoveryManifest.Package.Repository + "#" + recoveryManifest.Package.BuildID
	return Artifact{
		Identity: identity, CPV: cpv, Slot: recoveryManifest.Package.Slot,
		Subslot: recoveryManifest.Package.Subslot, Repository: recoveryManifest.Package.Repository,
		BuildID: recoveryManifest.Package.BuildID,
		Path:    filepath.ToSlash(relative), SHA256: artifactDigest, ManifestSHA256: manifestDigest,
	}, nil
}

func validateRequest(request Request) error {
	for _, field := range []struct{ name, value string }{
		{"set ID", request.SetID}, {"operation ID", request.OperationID},
	} {
		if field.value == "" || len(field.value) > 256 || strings.ContainsAny(field.value, `/\`+"\x00\r\n") || field.value == "." || field.value == ".." {
			return fmt.Errorf("recovery set: invalid %s", field.name)
		}
	}
	decoded, err := hex.DecodeString(request.PlanSHA256)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("recovery set: invalid plan digest")
	}
	if request.Directory == "" || request.RootDir == "" {
		return fmt.Errorf("recovery set: directory and ROOT are required")
	}
	if len(request.Packages) == 0 {
		return fmt.Errorf("recovery set: at least one package is required")
	}
	seen := make(map[string]struct{}, len(request.Packages))
	for _, pkg := range request.Packages {
		if pkg.VDBEntryPath == "" {
			return fmt.Errorf("recovery set: package VDB path is required")
		}
		clean := filepath.Clean(pkg.VDBEntryPath)
		if _, exists := seen[clean]; exists {
			return fmt.Errorf("recovery set: duplicate package VDB path %s", clean)
		}
		seen[clean] = struct{}{}
	}
	provenance := requestProvenance(request)
	// The capture API validates the complete provenance contract before writing
	// each artifact. Check required availability here before staging begins.
	if provenance.ConfigurationFingerprint.Scope == "" || provenance.RepositoryFingerprint.Scope == "" {
		return fmt.Errorf("recovery set: configuration and repository fingerprints are required")
	}
	if err := binpkg.ValidateCaptureProvenance(provenance); err != nil {
		return fmt.Errorf("recovery set: invalid capture provenance: %w", err)
	}
	return nil
}

func requestProvenance(request Request) binpkg.CaptureProvenance {
	return binpkg.CaptureProvenance{
		Schema: binpkg.CaptureContextSchema, OperationKind: "pre-update-recovery",
		OperationID: request.OperationID, RecoverySetID: request.SetID, PlanSHA256: request.PlanSHA256,
		ConfigurationFingerprint: request.ConfigurationFingerprint,
		RepositoryFingerprint:    request.RepositoryFingerprint,
	}
}

func recoveryObjectProvenance(request Request) binpkg.CaptureProvenance {
	return binpkg.CaptureProvenance{
		Schema:                   binpkg.CaptureContextSchema,
		OperationKind:            "recovery-object",
		ConfigurationFingerprint: request.ConfigurationFingerprint,
		RepositoryFingerprint:    request.RepositoryFingerprint,
	}
}

func validateManifest(manifest Manifest, expected int) error {
	planDigest, planErr := hex.DecodeString(manifest.PlanSHA256)
	if manifest.Schema != Schema || manifest.State != "complete" || manifest.SetID == "" ||
		manifest.OperationID == "" || len(manifest.Artifacts) == 0 || len(manifest.Artifacts) != expected ||
		planErr != nil || len(planDigest) != sha256.Size {
		return fmt.Errorf("recovery set: incomplete set manifest")
	}
	publicKey, publicErr := base64.StdEncoding.DecodeString(manifest.SignerPublicKey)
	signature, signatureErr := base64.StdEncoding.DecodeString(manifest.Signature)
	if manifest.SignatureAlgorithm != "Ed25519" || publicErr != nil || len(publicKey) != ed25519.PublicKeySize ||
		signatureErr != nil || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("recovery set: invalid manifest signature")
	}
	if manifest.Capture.OperationKind != "pre-update-recovery" ||
		manifest.Capture.RecoverySetID != manifest.SetID ||
		manifest.Capture.OperationID != manifest.OperationID ||
		manifest.Capture.PlanSHA256 != manifest.PlanSHA256 {
		return fmt.Errorf("recovery set: manifest capture provenance mismatch")
	}
	if err := binpkg.ValidateCaptureProvenance(manifest.Capture); err != nil {
		return fmt.Errorf("recovery set: invalid manifest capture provenance: %w", err)
	}
	previous := ""
	orders := make(map[int]struct{}, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		if artifact.Identity == "" || artifact.CPV == "" || artifact.Path == "" || filepath.IsAbs(artifact.Path) ||
			filepath.ToSlash(filepath.Clean(filepath.FromSlash(artifact.Path))) != artifact.Path ||
			artifact.Identity <= previous {
			return fmt.Errorf("recovery set: invalid or duplicate artifact entry")
		}
		if artifact.CaptureOrder < 0 || artifact.CaptureOrder >= expected {
			return fmt.Errorf("recovery set: invalid artifact capture order")
		}
		if _, exists := orders[artifact.CaptureOrder]; exists {
			return fmt.Errorf("recovery set: duplicate artifact capture order")
		}
		orders[artifact.CaptureOrder] = struct{}{}
		for _, digest := range []string{artifact.SHA256, artifact.ManifestSHA256} {
			decoded, err := hex.DecodeString(digest)
			if err != nil || len(decoded) != sha256.Size {
				return fmt.Errorf("recovery set: invalid artifact digest")
			}
		}
		previous = artifact.Identity
	}
	return nil
}

func Read(path string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("recovery set: read manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("recovery set: decode manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("recovery set: manifest has trailing JSON")
	}
	if err := validateManifest(manifest, len(manifest.Artifacts)); err != nil {
		return nil, err
	}
	if err := verifyStoreSignature(path, manifest); err != nil {
		return nil, err
	}
	for _, artifact := range manifest.Artifacts {
		artifactPath := filepath.Join(path, filepath.FromSlash(artifact.Path))
		digest, err := fileSHA256(artifactPath)
		if err != nil || digest != artifact.SHA256 {
			return nil, fmt.Errorf("recovery set: artifact %s digest mismatch", artifact.CPV)
		}
		recoveryManifest, err := binpkg.ReadRecoveryManifest(artifactPath)
		if err != nil {
			return nil, err
		}
		manifestDigest, err := recoveryManifest.SHA256()
		if err != nil || manifestDigest != artifact.ManifestSHA256 {
			return nil, fmt.Errorf("recovery set: artifact %s manifest digest mismatch", artifact.CPV)
		}
		if recoveryManifest.Capture.OperationKind != "recovery-object" ||
			recoveryManifest.Capture.ConfigurationFingerprint != manifest.Capture.ConfigurationFingerprint ||
			recoveryManifest.Capture.RepositoryFingerprint != manifest.Capture.RepositoryFingerprint ||
			recoveryManifest.Capture.OperationID != "" || recoveryManifest.Capture.RecoverySetID != "" ||
			recoveryManifest.Capture.PlanSHA256 != "" {
			return nil, fmt.Errorf("recovery set: artifact %s provenance mismatch", artifact.CPV)
		}
	}
	return &manifest, nil
}

func manifestSigningBytes(manifest Manifest) ([]byte, error) {
	manifest.Signature = ""
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("recovery set: encode manifest signature payload: %w", err)
	}
	return encoded, nil
}

func loadOrCreateStoreSigner(directory string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	privatePath := filepath.Join(directory, "signing.key")
	publicPath := filepath.Join(directory, "trusted.pub")
	if err := os.MkdirAll(directory, 0755); err != nil {
		return nil, nil, fmt.Errorf("recovery set: create signing store: %w", err)
	}
	for {
		encoded, err := os.ReadFile(privatePath)
		if err == nil {
			privateKey, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
			if decodeErr != nil || len(privateKey) != ed25519.PrivateKeySize {
				return nil, nil, fmt.Errorf("recovery set: invalid signing key")
			}
			info, statErr := os.Stat(privatePath)
			if statErr != nil || info.Mode().Perm()&0077 != 0 {
				return nil, nil, fmt.Errorf("recovery set: signing key permissions must be 0600")
			}
			publicKey := ed25519.PrivateKey(privateKey).Public().(ed25519.PublicKey)
			if err := ensureTrustedPublicKey(publicPath, publicKey); err != nil {
				return nil, nil, err
			}
			return ed25519.PrivateKey(privateKey), publicKey, nil
		}
		if !os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("recovery set: read signing key: %w", err)
		}
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, nil, fmt.Errorf("recovery set: generate signing key: %w", err)
		}
		file, err := os.OpenFile(privatePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("recovery set: create signing key: %w", err)
		}
		payload := []byte(base64.StdEncoding.EncodeToString(privateKey) + "\n")
		if _, err := file.Write(payload); err != nil {
			_ = file.Close()
			return nil, nil, fmt.Errorf("recovery set: write signing key: %w", err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return nil, nil, fmt.Errorf("recovery set: sync signing key: %w", err)
		}
		if err := file.Close(); err != nil {
			return nil, nil, fmt.Errorf("recovery set: close signing key: %w", err)
		}
		if err := ensureTrustedPublicKey(publicPath, publicKey); err != nil {
			return nil, nil, err
		}
		if err := syncDirectory(directory); err != nil {
			return nil, nil, fmt.Errorf("recovery set: sync signing store: %w", err)
		}
		return privateKey, publicKey, nil
	}
}

func ensureTrustedPublicKey(path string, publicKey ed25519.PublicKey) error {
	expected := base64.StdEncoding.EncodeToString(publicKey)
	data, err := os.ReadFile(path)
	if err == nil {
		if strings.TrimSpace(string(data)) != expected {
			return fmt.Errorf("recovery set: signing key does not match trusted public key")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("recovery set: read trusted public key: %w", err)
	}
	if err := writeDurableFile(path, []byte(expected+"\n")); err != nil && !os.IsExist(err) {
		return fmt.Errorf("recovery set: publish trusted public key: %w", err)
	}
	data, err = os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) != expected {
		return fmt.Errorf("recovery set: trusted public key publication raced with a different key")
	}
	return nil
}

func verifyStoreSignature(setPath string, manifest Manifest) error {
	store := filepath.Dir(filepath.Dir(filepath.Clean(setPath)))
	trusted, err := os.ReadFile(filepath.Join(store, "trusted.pub"))
	if err != nil {
		return fmt.Errorf("recovery set: read trusted public key: %w", err)
	}
	publicKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(trusted)))
	if err != nil || len(publicKey) != ed25519.PublicKeySize ||
		manifest.SignerPublicKey != base64.StdEncoding.EncodeToString(publicKey) {
		return fmt.Errorf("recovery set: manifest signer is not trusted")
	}
	signature, err := base64.StdEncoding.DecodeString(manifest.Signature)
	if err != nil {
		return fmt.Errorf("recovery set: invalid manifest signature encoding")
	}
	signingBytes, err := manifestSigningBytes(manifest)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, signingBytes, signature) {
		return fmt.Errorf("recovery set: manifest signature verification failed")
	}
	return nil
}

func materializeObject(directory, artifactPath, digest string) error {
	objectsDir := filepath.Join(directory, "objects", "sha256")
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		return fmt.Errorf("recovery set: create object directory: %w", err)
	}
	objectPath := filepath.Join(objectsDir, digest)
	if err := os.Link(artifactPath, objectPath); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("recovery set: publish content object: %w", err)
		}
		existingDigest, digestErr := fileSHA256(objectPath)
		if digestErr != nil || existingDigest != digest {
			return fmt.Errorf("recovery set: existing content object %s failed verification", digest)
		}
	}
	if err := os.Chmod(objectPath, 0444); err != nil {
		return fmt.Errorf("recovery set: make content object immutable: %w", err)
	}
	if err := os.Remove(artifactPath); err != nil {
		return fmt.Errorf("recovery set: replace staged artifact with object link: %w", err)
	}
	if err := os.Link(objectPath, artifactPath); err != nil {
		return fmt.Errorf("recovery set: link content object into set: %w", err)
	}
	if err := syncDirectory(objectsDir); err != nil {
		return fmt.Errorf("recovery set: sync object directory: %w", err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("recovery set: open artifact: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", fmt.Errorf("recovery set: hash artifact: %w", copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("recovery set: close artifact: %w", closeErr)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeDurableFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("recovery set: create manifest: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("recovery set: write manifest: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("recovery set: sync manifest: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("recovery set: close manifest: %w", err)
	}
	return nil
}

func syncDirectories(root string) error {
	var directories []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			directories = append(directories, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("recovery set: inspect staging tree for sync: %w", err)
	}
	sort.Slice(directories, func(i, j int) bool {
		return strings.Count(directories[i], string(filepath.Separator)) >
			strings.Count(directories[j], string(filepath.Separator))
	})
	for _, directory := range directories {
		if err := syncDirectory(directory); err != nil {
			return fmt.Errorf("recovery set: sync directory %s: %w", directory, err)
		}
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
