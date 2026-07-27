package binpkg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const CaptureContextSchema = 1

type CaptureRequest struct {
	VDBEntryPath string
	RootDir      string
	PackageDir   string
	Provenance   CaptureProvenance
}

type CaptureProvenance struct {
	Schema                   int              `json:"schema"`
	OperationKind            string           `json:"operation_kind"`
	OperationID              string           `json:"operation_id,omitempty"`
	RecoverySetID            string           `json:"recovery_set_id,omitempty"`
	PlanSHA256               string           `json:"plan_sha256,omitempty"`
	ConfigurationFingerprint InputFingerprint `json:"configuration_fingerprint"`
	RepositoryFingerprint    InputFingerprint `json:"repository_fingerprint"`
}

type InputFingerprint struct {
	Scope             string `json:"scope"`
	SHA256            string `json:"sha256,omitempty"`
	Complete          bool   `json:"complete"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

type FingerprintInput struct {
	Label string
	Path  string
}

func LegacyCaptureProvenance() CaptureProvenance {
	return CaptureProvenance{
		Schema:        CaptureContextSchema,
		OperationKind: "legacy-create-api",
		ConfigurationFingerprint: InputFingerprint{
			Scope: "portage-configuration", UnavailableReason: "not provided by legacy capture API",
		},
		RepositoryFingerprint: InputFingerprint{
			Scope: "repository-identity", UnavailableReason: "not provided by legacy capture API",
		},
	}
}

func FingerprintConfiguration(path string) (InputFingerprint, error) {
	digest, err := fingerprintPaths(path, []string{"."})
	if err != nil {
		return InputFingerprint{}, err
	}
	return InputFingerprint{Scope: "portage-configuration", SHA256: digest, Complete: true}, nil
}

func FingerprintRepositoryIdentity(path string) (InputFingerprint, error) {
	candidates := []string{"profiles/repo_name", "metadata/layout.conf", "metadata/timestamp.chk", ".git/HEAD"}
	head, err := os.ReadFile(filepath.Join(path, ".git", "HEAD"))
	if err == nil {
		value := strings.TrimSpace(string(head))
		if strings.HasPrefix(value, "ref: ") {
			ref := strings.TrimSpace(strings.TrimPrefix(value, "ref: "))
			if ref != "" && !filepath.IsAbs(ref) && filepath.Clean(ref) == filepath.FromSlash(ref) &&
				ref != ".." && !strings.HasPrefix(ref, ".."+string(filepath.Separator)) {
				candidates = append(candidates, filepath.ToSlash(filepath.Join(".git", ref)))
			}
		}
	}
	digest, err := fingerprintPaths(path, candidates)
	if err != nil {
		return InputFingerprint{}, err
	}
	return InputFingerprint{
		Scope: "repository-identity", SHA256: digest, Complete: false,
		UnavailableReason: "standalone capture fingerprints repository identity markers, not a selected source closure",
	}, nil
}

func FingerprintSelectedSourceClosure(inputs []FingerprintInput) (InputFingerprint, error) {
	if len(inputs) == 0 {
		return InputFingerprint{}, fmt.Errorf("binpkg: selected source closure is empty")
	}
	ordered := append([]FingerprintInput(nil), inputs...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Label == ordered[j].Label {
			return filepath.Clean(ordered[i].Path) < filepath.Clean(ordered[j].Path)
		}
		return ordered[i].Label < ordered[j].Label
	})
	hash := sha256.New()
	seen := make(map[string]struct{}, len(ordered))
	for _, input := range ordered {
		if input.Label == "" || strings.ContainsAny(input.Label, "\x00\r\n") || input.Path == "" {
			return InputFingerprint{}, fmt.Errorf("binpkg: invalid selected source input")
		}
		identity := input.Label + "\x00" + filepath.Clean(input.Path)
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		digest, err := fingerprintPaths(input.Path, []string{"."})
		if err != nil {
			return InputFingerprint{}, err
		}
		if _, err := io.WriteString(hash, input.Label+"\x00"+digest+"\x00"); err != nil {
			return InputFingerprint{}, err
		}
	}
	return InputFingerprint{
		Scope:  "selected-repository-source-closure",
		SHA256: hex.EncodeToString(hash.Sum(nil)), Complete: true,
	}, nil
}

func fingerprintPaths(root string, relativePaths []string) (string, error) {
	hash := sha256.New()
	cleanRoot := filepath.Clean(root)
	ordered := append([]string(nil), relativePaths...)
	sort.Strings(ordered)
	seen := make(map[string]struct{}, len(ordered))
	for _, relative := range ordered {
		clean := filepath.Clean(filepath.FromSlash(relative))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("binpkg: fingerprint path %q escapes its root", relative)
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		if err := hashFingerprintPath(hash, cleanRoot, clean); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func hashFingerprintPath(hash io.Writer, root, relative string) error {
	path := filepath.Join(root, relative)
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		_, err = io.WriteString(hash, filepath.ToSlash(relative)+"\x00missing\x00")
		return err
	}
	if err != nil {
		return fmt.Errorf("binpkg: fingerprint %s: %w", path, err)
	}
	return filepath.Walk(path, func(current string, currentInfo os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryRelative, err := filepath.Rel(root, current)
		if err != nil || entryRelative == ".." || strings.HasPrefix(entryRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(entryRelative) {
			return fmt.Errorf("binpkg: fingerprint entry %s escapes root", current)
		}
		if _, err := io.WriteString(hash, filepath.ToSlash(entryRelative)+"\x00"+strconv.FormatUint(uint64(currentInfo.Mode()), 10)+"\x00"); err != nil {
			return err
		}
		switch {
		case currentInfo.Mode().IsRegular():
			file, err := os.Open(current)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case currentInfo.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(current)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(hash, target); err != nil {
				return err
			}
		case currentInfo.IsDir():
		default:
			return fmt.Errorf("binpkg: fingerprint path %s has unsupported type %s", current, currentInfo.Mode().Type())
		}
		_, err = io.WriteString(hash, "\x00")
		return err
	})
}

func validateCaptureProvenance(provenance CaptureProvenance) error {
	if provenance.Schema != CaptureContextSchema {
		return fmt.Errorf("binpkg: unsupported capture context schema %d", provenance.Schema)
	}
	if provenance.OperationKind == "" || strings.ContainsAny(provenance.OperationKind, "\x00\r\n") {
		return fmt.Errorf("binpkg: invalid capture operation kind")
	}
	for _, field := range []struct{ name, value string }{
		{"operation ID", provenance.OperationID}, {"recovery set ID", provenance.RecoverySetID},
	} {
		if len(field.value) > 256 || strings.ContainsAny(field.value, "\x00\r\n") {
			return fmt.Errorf("binpkg: invalid capture %s", field.name)
		}
	}
	if provenance.PlanSHA256 != "" {
		decoded, err := hex.DecodeString(provenance.PlanSHA256)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("binpkg: invalid capture plan digest")
		}
	}
	for _, field := range []struct {
		name        string
		fingerprint InputFingerprint
	}{
		{"configuration", provenance.ConfigurationFingerprint},
		{"repository", provenance.RepositoryFingerprint},
	} {
		name, fingerprint := field.name, field.fingerprint
		if fingerprint.Scope == "" {
			return fmt.Errorf("binpkg: %s fingerprint has no scope", name)
		}
		if fingerprint.SHA256 == "" {
			if fingerprint.Complete || fingerprint.UnavailableReason == "" {
				return fmt.Errorf("binpkg: unavailable %s fingerprint lacks an explicit reason", name)
			}
			continue
		}
		decoded, err := hex.DecodeString(fingerprint.SHA256)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("binpkg: invalid %s fingerprint digest", name)
		}
		if fingerprint.Complete && fingerprint.UnavailableReason != "" {
			return fmt.Errorf("binpkg: complete %s fingerprint has an unavailable reason", name)
		}
		if !fingerprint.Complete && fingerprint.UnavailableReason == "" {
			return fmt.Errorf("binpkg: partial %s fingerprint lacks an explanation", name)
		}
	}
	return nil
}

func ValidateCaptureProvenance(provenance CaptureProvenance) error {
	return validateCaptureProvenance(provenance)
}
