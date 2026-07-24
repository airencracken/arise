package distfiles

import (
	"bufio"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// Plan joins enabled SRC_URI entries to their authoritative Manifest records.
// Multiple URIs resolving to one destination become ordered fallbacks.
func Plan(reader io.Reader, srcURI string, use map[string]bool) ([]Artifact, error) {
	manifest, err := ParseManifest(reader)
	if err != nil {
		return nil, err
	}
	sources := selectedSourceList(strings.Fields(srcURI), use)
	ordered := make([]string, 0, len(sources))
	byName := make(map[string]*Artifact)
	for _, source := range sources {
		artifact, exists := manifest[source.name]
		if !exists {
			return nil, fmt.Errorf("manifest: selected distfile %s has no DIST record", source.name)
		}
		planned := byName[source.name]
		if planned == nil {
			copy := artifact
			byName[source.name] = &copy
			planned = &copy
			ordered = append(ordered, source.name)
		}
		planned.Sources = append(planned.Sources, source.uri)
		byName[source.name] = planned
	}
	result := make([]Artifact, 0, len(ordered))
	for _, name := range ordered {
		result = append(result, *byName[name])
	}
	return result, nil
}

type selectedSource struct{ uri, name string }

func selectedSourceList(tokens []string, use map[string]bool) []selectedSource {
	result, _ := parseSourceTokens(tokens, 0, true, use)
	return result
}

func parseSourceTokens(tokens []string, index int, enabled bool, use map[string]bool) ([]selectedSource, int) {
	var result []selectedSource
	for index < len(tokens) {
		token := tokens[index]
		if token == ")" {
			return result, index + 1
		}
		if strings.HasSuffix(token, "?") && index+1 < len(tokens) && tokens[index+1] == "(" {
			flag := strings.TrimSuffix(token, "?")
			condition := use[strings.TrimPrefix(flag, "!")]
			if strings.HasPrefix(flag, "!") {
				condition = !condition
			}
			child, next := parseSourceTokens(tokens, index+2, enabled && condition, use)
			result = append(result, child...)
			index = next
			continue
		}
		if enabled && token != "(" && token != "||" && token != "->" {
			name := sourceFilename(token)
			if index+2 < len(tokens) && tokens[index+1] == "->" {
				name = tokens[index+2]
				index += 2
			}
			if name != "" {
				result = append(result, selectedSource{uri: token, name: name})
			}
		}
		index++
	}
	return result, index
}

func sourceFilename(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return path.Base(parsed.Path)
}

// Artifact is the immutable identity of one distfile. Sources are ordered
// fallback locations; Name, Size, and Digests come from the package Manifest.
type Artifact struct {
	Name    string
	Size    int64
	Digests map[string]string
	Sources []string
}

// VerifiedSet is the only distfile collection that may cross into the ebuild
// execution ABI.
type VerifiedSet struct {
	Directory string
	Artifacts []Artifact
}

func (s VerifiedSet) Paths() []string {
	paths := make([]string, 0, len(s.Artifacts))
	for _, artifact := range s.Artifacts {
		paths = append(paths, filepath.Join(s.Directory, artifact.Name))
	}
	return paths
}

// ParseManifest reads DIST records and rejects ambiguous duplicate identities.
func ParseManifest(reader io.Reader) (map[string]Artifact, error) {
	artifacts := make(map[string]Artifact)
	scanner := bufio.NewScanner(reader)
	line := 0
	for scanner.Scan() {
		line++
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || fields[0] != "DIST" {
			continue
		}
		if len(fields) < 5 || len(fields)%2 == 0 {
			return nil, fmt.Errorf("manifest: line %d has malformed DIST record", line)
		}
		name := fields[1]
		if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
			return nil, fmt.Errorf("manifest: line %d has unsafe distfile name %q", line, name)
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 {
			return nil, fmt.Errorf("manifest: line %d has invalid size %q", line, fields[2])
		}
		digests := make(map[string]string)
		for index := 3; index < len(fields); index += 2 {
			algorithm := strings.ToUpper(fields[index])
			digest := strings.ToLower(fields[index+1])
			if _, err := hex.DecodeString(digest); err != nil {
				return nil, fmt.Errorf("manifest: line %d has invalid %s digest", line, algorithm)
			}
			digests[algorithm] = digest
		}
		artifact := Artifact{Name: name, Size: size, Digests: digests}
		if previous, exists := artifacts[name]; exists && !sameIdentity(previous, artifact) {
			return nil, fmt.Errorf("manifest: conflicting DIST records for %s", name)
		}
		artifacts[name] = artifact
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("manifest: read: %w", err)
	}
	return artifacts, nil
}

func sameIdentity(left, right Artifact) bool {
	if left.Name != right.Name || left.Size != right.Size || len(left.Digests) != len(right.Digests) {
		return false
	}
	for algorithm, digest := range left.Digests {
		if right.Digests[algorithm] != digest {
			return false
		}
	}
	return true
}

// Verify checks size and every supported digest recorded for an artifact. It
// fails closed when the Manifest contains no digest Arise can verify.
func Verify(path string, artifact Artifact) error {
	if artifact.Name == "" || filepath.Base(artifact.Name) != artifact.Name || artifact.Name == "." || artifact.Name == ".." {
		return fmt.Errorf("distfile: unsafe artifact name %q", artifact.Name)
	}
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("distfile %s: lstat: %w", artifact.Name, err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 || !linkInfo.Mode().IsRegular() {
		return fmt.Errorf("distfile %s: cache entry is not a regular file", artifact.Name)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("distfile %s: open: %w", artifact.Name, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("distfile %s: stat: %w", artifact.Name, err)
	}
	if info.Size() != artifact.Size {
		return fmt.Errorf("distfile %s: size %d, want %d", artifact.Name, info.Size(), artifact.Size)
	}
	algorithms := make([]string, 0, len(artifact.Digests))
	for algorithm := range artifact.Digests {
		algorithms = append(algorithms, algorithm)
	}
	sort.Strings(algorithms)
	type verifier struct {
		algorithm string
		expected  string
		hash      hash.Hash
	}
	verifiers := make([]verifier, 0, len(algorithms))
	for _, algorithm := range algorithms {
		var digest hash.Hash
		switch algorithm {
		case "BLAKE2B":
			digest, _ = blake2b.New512(nil)
		case "SHA256":
			digest = sha256.New()
		case "SHA512":
			digest = sha512.New()
		default:
			continue
		}
		verifiers = append(verifiers, verifier{algorithm: algorithm, expected: artifact.Digests[algorithm], hash: digest})
	}
	if len(verifiers) == 0 {
		return fmt.Errorf("distfile %s: Manifest has no supported digest", artifact.Name)
	}
	writers := make([]io.Writer, 0, len(verifiers))
	for i := range verifiers {
		writers = append(writers, verifiers[i].hash)
	}
	if _, err := io.Copy(io.MultiWriter(writers...), file); err != nil {
		return err
	}
	for _, verifier := range verifiers {
		if hex.EncodeToString(verifier.hash.Sum(nil)) != verifier.expected {
			return fmt.Errorf("distfile %s: %s digest mismatch", artifact.Name, verifier.algorithm)
		}
	}
	return nil
}
