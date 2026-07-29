package vendorartifact

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const Schema = "arise-vendor-manifest-v1"

type Module struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

type Manifest struct {
	Schema          string   `json:"schema"`
	Project         string   `json:"project"`
	Version         string   `json:"version"`
	SourceCommit    string   `json:"source_commit"`
	SourceDateEpoch int64    `json:"source_date_epoch"`
	GoVersion       string   `json:"go_version"`
	GoModSHA256     string   `json:"go_mod_sha256"`
	GoSumSHA256     string   `json:"go_sum_sha256"`
	VendorSHA256    string   `json:"vendor_tree_sha256"`
	Modules         []Module `json:"modules"`
}

func Create(root, version, commit, goVersion string, epoch int64) (Manifest, error) {
	if version == "" || commit == "" || epoch <= 0 {
		return Manifest{}, errors.New("version, source commit, and positive source date epoch are required")
	}
	modHash, err := fileHash(filepath.Join(root, "go.mod"))
	if err != nil {
		return Manifest{}, err
	}
	sumHash, err := fileHash(filepath.Join(root, "go.sum"))
	if err != nil {
		return Manifest{}, err
	}
	vendorHash, err := TreeHash(filepath.Join(root, "vendor"))
	if err != nil {
		return Manifest{}, err
	}
	modules, err := readModules(filepath.Join(root, "vendor", "modules.txt"))
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{
		Schema: Schema, Project: "github.com/airencracken/arise", Version: version,
		SourceCommit: commit, SourceDateEpoch: epoch, GoVersion: goVersion,
		GoModSHA256: modHash, GoSumSHA256: sumHash, VendorSHA256: vendorHash, Modules: modules,
	}, nil
}

func Encode(w io.Writer, manifest Manifest) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(manifest)
}

func Decode(r io.Reader) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("manifest contains trailing data")
	}
	return manifest, nil
}

func Verify(root string, expected Manifest) error {
	if expected.Schema != Schema || expected.Project != "github.com/airencracken/arise" {
		return errors.New("unsupported vendor manifest")
	}
	actual, err := Create(root, expected.Version, expected.SourceCommit, expected.GoVersion, expected.SourceDateEpoch)
	if err != nil {
		return err
	}
	if actual.GoModSHA256 != expected.GoModSHA256 ||
		actual.GoSumSHA256 != expected.GoSumSHA256 ||
		actual.VendorSHA256 != expected.VendorSHA256 {
		return errors.New("vendor artifact does not match its manifest")
	}
	if len(actual.Modules) != len(expected.Modules) {
		return errors.New("vendor module list does not match its manifest")
	}
	for i := range actual.Modules {
		if actual.Modules[i] != expected.Modules[i] {
			return errors.New("vendor module list does not match its manifest")
		}
	}
	return nil
}

func VerifyIdentity(manifest Manifest, version, commit string) error {
	if version == "" || commit == "" {
		return errors.New("expected version and source commit are required")
	}
	if manifest.Version != version || manifest.SourceCommit != commit {
		return errors.New("vendor manifest identity does not match the release")
	}
	return nil
}

func TreeHash(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
			return fmt.Errorf("unsafe vendor entry %q", path)
		}
		if entry.Type().IsRegular() {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			paths = append(paths, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, relative := range paths {
		path := filepath.Join(root, filepath.FromSlash(relative))
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%d:%s:%d:", len(relative), relative, len(data))
		_, _ = hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func readModules(path string) ([]Module, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var modules []Module
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 && fields[0] == "#" && fields[1] != "=>" {
			modules = append(modules, Module{Path: fields[1], Version: fields[2]})
		}
	}
	return modules, scanner.Err()
}
