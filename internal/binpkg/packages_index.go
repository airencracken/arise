package binpkg

import (
	"bufio"
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"golang.org/x/crypto/blake2b"
)

const (
	maxPackagesIndexBytes   = 64 << 20
	maxPackagesIndexRecords = 1_000_000
	maxPackagesIndexLine    = 1 << 20
)

type PackagesIndex struct {
	Header   map[string]string
	Packages []PackageIndexEntry
}

type PackageIndexEntry map[string]string

func ParsePackagesIndex(reader io.Reader) (*PackagesIndex, error) {
	limited := &io.LimitedReader{R: reader, N: maxPackagesIndexBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxPackagesIndexLine)
	var records []map[string]string
	current := make(map[string]string)
	flush := func() error {
		if len(current) == 0 {
			return nil
		}
		copyRecord := current
		records = append(records, copyRecord)
		current = make(map[string]string)
		if len(records) > maxPackagesIndexRecords+1 {
			return fmt.Errorf("binpkg: Packages index has too many records")
		}
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || key == "" || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\x00\r\n") {
			return nil, fmt.Errorf("binpkg: malformed Packages index line")
		}
		value = strings.TrimPrefix(value, " ")
		if strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("binpkg: invalid Packages index value")
		}
		if _, exists := current[key]; exists {
			return nil, fmt.Errorf("binpkg: duplicate Packages index key %q", key)
		}
		current[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("binpkg: read Packages index: %w", err)
	}
	if limited.N <= 0 {
		return nil, fmt.Errorf("binpkg: Packages index exceeds size limit")
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("binpkg: Packages index is empty")
	}
	index := &PackagesIndex{Header: records[0]}
	inherited := []string{"ARCH", "CHOST", "CBUILD", "CONFIG_PROTECT", "CONFIG_PROTECT_MASK"}
	seenInstances := make(map[string]struct{})
	for _, record := range records[1:] {
		cpv := record["CPV"]
		path := record["PATH"]
		if cpv == "" || path == "" {
			return nil, fmt.Errorf("binpkg: Packages entry lacks CPV or PATH")
		}
		parsed, parseErr := atom.Parse(cpv)
		if parseErr != nil || parsed.Version == nil {
			return nil, fmt.Errorf("binpkg: invalid Packages CPV %q", cpv)
		}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
		if clean != path || filepath.IsAbs(path) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("binpkg: unsafe Packages PATH %q", path)
		}
		for _, key := range inherited {
			if record[key] == "" && index.Header[key] != "" {
				record[key] = index.Header[key]
			}
		}
		instance := cpv + "\x00" + record["BUILD_ID"] + "\x00" + path
		if _, exists := seenInstances[instance]; exists {
			return nil, fmt.Errorf("binpkg: duplicate Packages instance")
		}
		if buildID := record["BUILD_ID"]; buildID != "" {
			if _, err := strconv.ParseUint(buildID, 10, 64); err != nil {
				return nil, fmt.Errorf("binpkg: invalid Packages BUILD_ID")
			}
		}
		if size := record["SIZE"]; size != "" {
			if parsedSize, err := strconv.ParseInt(size, 10, 64); err != nil || parsedSize < 0 {
				return nil, fmt.Errorf("binpkg: invalid Packages SIZE")
			}
		}
		for key, expectedBytes := range map[string]int{"SHA512": sha512.Size, "BLAKE2B": blake2b.Size} {
			if digest := record[key]; digest != "" {
				decoded, err := hex.DecodeString(digest)
				if err != nil || len(decoded) != expectedBytes {
					return nil, fmt.Errorf("binpkg: invalid Packages %s digest", key)
				}
			}
		}
		seenInstances[instance] = struct{}{}
		index.Packages = append(index.Packages, PackageIndexEntry(record))
	}
	if declared := index.Header["PACKAGES"]; declared != "" {
		count, err := strconv.Atoi(declared)
		if err != nil || count != len(index.Packages) {
			return nil, fmt.Errorf("binpkg: Packages count mismatch")
		}
	}
	return index, nil
}

func ReadPackagesIndex(path string) (*PackagesIndex, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("binpkg: open Packages index: %w", err)
	}
	defer file.Close()
	return ParsePackagesIndex(file)
}

func (index *PackagesIndex) Encode(timestamp time.Time) ([]byte, error) {
	if index == nil {
		return nil, fmt.Errorf("binpkg: nil Packages index")
	}
	header := cloneStringMap(index.Header)
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	header["TIMESTAMP"] = strconv.FormatInt(timestamp.Unix(), 10)
	header["PACKAGES"] = strconv.Itoa(len(index.Packages))
	var output bytes.Buffer
	writeIndexRecord(&output, header)
	packages := append([]PackageIndexEntry(nil), index.Packages...)
	sort.Slice(packages, func(i, j int) bool {
		if packages[i]["CPV"] != packages[j]["CPV"] {
			return packages[i]["CPV"] < packages[j]["CPV"]
		}
		if packages[i]["BUILD_ID"] != packages[j]["BUILD_ID"] {
			return packages[i]["BUILD_ID"] < packages[j]["BUILD_ID"]
		}
		return packages[i]["PATH"] < packages[j]["PATH"]
	})
	for _, entry := range packages {
		if entry["CPV"] == "" || entry["PATH"] == "" {
			return nil, fmt.Errorf("binpkg: Packages entry lacks CPV or PATH")
		}
		writeIndexRecord(&output, map[string]string(entry))
	}
	return output.Bytes(), nil
}

func writeIndexRecord(output *bytes.Buffer, record map[string]string) {
	keys := make([]string, 0, len(record))
	for key, value := range record {
		if value != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(output, "%s: %s\n", key, record[key])
	}
	output.WriteByte('\n')
}

func WritePackagesIndex(path string, index *PackagesIndex, timestamp time.Time) error {
	data, err := index.Encode(timestamp)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".Packages.tmp-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	published = true
	return syncBinpkgDirectory(filepath.Dir(path))
}

func NewPackageIndexEntry(packagePath, root string, info *BinPkgInfo, metadata map[string][]byte) (PackageIndexEntry, error) {
	if info == nil {
		return nil, fmt.Errorf("binpkg: package information is required")
	}
	relative, err := filepath.Rel(root, packagePath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return nil, fmt.Errorf("binpkg: package path is outside index root")
	}
	file, err := os.Open(packagePath)
	if err != nil {
		return nil, err
	}
	shaHash := sha512.New()
	blakeHash, _ := blake2b.New512(nil)
	_, copyErr := io.Copy(io.MultiWriter(shaHash, blakeHash), file)
	closeErr := file.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	stat, err := os.Stat(packagePath)
	if err != nil {
		return nil, err
	}
	entry := PackageIndexEntry{
		"CPV": info.CPV(), "PATH": filepath.ToSlash(relative),
		"SIZE":    strconv.FormatInt(stat.Size(), 10),
		"SHA512":  hex.EncodeToString(shaHash.Sum(nil)),
		"BLAKE2B": hex.EncodeToString(blakeHash.Sum(nil)),
		"SLOT":    info.Slot, "USE": info.Use, "EAPI": info.EAPI,
	}
	for _, key := range []string{"BUILD_ID", "BUILD_TIME", "repository", "CHOST", "CBUILD", "ABI", "IUSE", "RDEPEND", "PDEPEND"} {
		if value := strings.TrimSpace(string(metadata[key])); value != "" {
			entry[key] = value
		}
	}
	return entry, nil
}

func SelectPackageInstance(entries []PackageIndexEntry, cpv, buildID string) (PackageIndexEntry, error) {
	var matches []PackageIndexEntry
	for _, entry := range entries {
		if entry["CPV"] == cpv && (buildID == "" || entry["BUILD_ID"] == buildID) {
			matches = append(matches, entry)
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.Slice(matches, func(i, j int) bool {
		left, leftErr := strconv.ParseUint(matches[i]["BUILD_ID"], 10, 64)
		right, rightErr := strconv.ParseUint(matches[j]["BUILD_ID"], 10, 64)
		if leftErr == nil && rightErr == nil && left != right {
			return left > right
		}
		if matches[i]["BUILD_TIME"] != matches[j]["BUILD_TIME"] {
			return matches[i]["BUILD_TIME"] > matches[j]["BUILD_TIME"]
		}
		return matches[i]["PATH"] < matches[j]["PATH"]
	})
	if buildID != "" && len(matches) > 1 {
		return nil, fmt.Errorf("binpkg: ambiguous package instance %s build %s", cpv, buildID)
	}
	return matches[0], nil
}

func cloneStringMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
