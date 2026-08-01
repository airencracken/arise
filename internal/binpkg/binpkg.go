package binpkg

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"golang.org/x/crypto/blake2b"
)

const xpakMagic = "XPAKSTOP"
const xpakTrailerLen = 4096
const maxXPAKMetadataBytes = 64 << 20

type ExtractionPolicy struct {
	MaxEntries        int
	MaxTotalBytes     int64
	MaxFileBytes      int64
	MaxXAttrBytes     int64
	PreserveOwnership bool
}

var DefaultExtractionPolicy = ExtractionPolicy{
	MaxEntries: 1_000_000, MaxTotalBytes: 64 << 30, MaxFileBytes: 16 << 30,
	MaxXAttrBytes:     16 << 20,
	PreserveOwnership: os.Geteuid() == 0,
}

type BinPkgInfo struct {
	Category   string
	Package    string
	Version    string
	Slot       string
	Subslot    string
	Use        string
	EAPI       string
	BuildTime  int64
	BuildID    string
	Repository string
	CHOST      string
	ABI        string
	IUse       string
	Size       int64
	Path       string
}

func (b *BinPkgInfo) CPV() string {
	return b.Category + "/" + b.Package + "-" + b.Version
}

func (b *BinPkgInfo) CP() string {
	return b.Category + "/" + b.Package
}

type Compression int

const (
	CompressionAuto Compression = iota
	CompressionBzip2
	CompressionXz
	CompressionNone
)

func detectCompression(path string) Compression {
	switch {
	case strings.HasSuffix(path, ".tbz2"):
		return CompressionBzip2
	case strings.HasSuffix(path, ".txz"):
		return CompressionXz
	case strings.HasSuffix(path, ".xpak"):
		return CompressionBzip2
	case strings.HasSuffix(path, ".tar"):
		return CompressionNone
	default:
		return CompressionBzip2
	}
}

func ReadInfo(path string) (*BinPkgInfo, error) {
	if IsGPKG(path) {
		pkg, err := ReadGPKG(context.Background(), path)
		if err != nil {
			return nil, err
		}
		meta := make(map[string]string, len(pkg.Metadata))
		for key, value := range pkg.Metadata {
			meta[key] = strings.TrimSpace(string(value))
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		result := &BinPkgInfo{Path: path, Size: info.Size()}
		result.Category = meta["CATEGORY"]
		result.Package = meta["PACKAGE"]
		result.Version = meta["VERSION"]
		result.Slot, result.Subslot = parseSlot(meta["SLOT"])
		result.Use, result.EAPI = meta["USE"], meta["EAPI"]
		result.BuildTime, _ = strconv.ParseInt(meta["BUILD_TIME"], 10, 64)
		result.BuildID, result.Repository = meta["BUILD_ID"], meta["repository"]
		result.CHOST, result.ABI, result.IUse = meta["CHOST"], meta["ABI"], meta["IUSE"]
		if result.Package == "" || result.Version == "" {
			pf := meta["PF"]
			if parsed, parseErr := atom.Parse(result.Category + "/" + pf); parseErr == nil && parsed.Version != nil {
				result.Package, result.Version = parsed.Package, parsed.Version.Raw
			}
		}
		return result, nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("binpkg: could not read information for %s: %w", path, err)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("binpkg: could not open %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil { /* Best effort */
		}
	}()

	meta, err := readXPAKMetadata(f, fi.Size())
	if err != nil {
		return nil, fmt.Errorf("binpkg: could not read package metadata from %s: %w", path, err)
	}

	info := &BinPkgInfo{
		Path: path,
		Size: fi.Size(),
	}
	info.Category = meta["CATEGORY"]
	info.Package = meta["PACKAGE"]
	info.Version = meta["VERSION"]

	if v, ok := meta["SLOT"]; ok {
		info.Slot, info.Subslot = parseSlot(v)
	}
	info.Use = meta["USE"]
	info.EAPI = meta["EAPI"]
	if bt, ok := meta["BUILD_TIME"]; ok {
		info.BuildTime, _ = strconv.ParseInt(bt, 10, 64)
	}
	info.BuildID, info.Repository = meta["BUILD_ID"], meta["repository"]
	info.CHOST, info.ABI, info.IUse = meta["CHOST"], meta["ABI"], meta["IUSE"]

	if info.Version == "" {
		pf := meta["PF"]
		if pf != "" {
			dash := strings.LastIndexByte(pf, '-')
			if dash >= 0 {
				info.Package = pf[:dash]
				info.Version = pf[dash+1:]
			}
		}
	}

	return info, nil
}

func readXPAKMetadata(f *os.File, size int64) (map[string]string, error) {
	metadata, recognized, err := readStandardXPAKMetadata(f, size)
	if recognized || err != nil {
		return metadata, err
	}
	return readLegacyXPAKMetadata(f, size)
}

func readStandardXPAKMetadata(f *os.File, size int64) (map[string]string, bool, error) {
	if size < 24 {
		return nil, false, nil
	}
	trailer := make([]byte, 16)
	if _, err := f.ReadAt(trailer, size-16); err != nil {
		return nil, true, err
	}
	if string(trailer[:8]) != "XPAKSTOP" || string(trailer[12:]) != "STOP" {
		return nil, false, nil
	}
	segmentSize := int64(binary.BigEndian.Uint32(trailer[8:12]))
	if segmentSize < 24 || segmentSize > maxXPAKMetadataBytes || segmentSize+8 > size {
		return nil, true, fmt.Errorf("binpkg: invalid XPAK segment size")
	}
	segment := make([]byte, segmentSize)
	if _, err := f.ReadAt(segment, size-segmentSize-8); err != nil {
		return nil, true, err
	}
	if string(segment[:8]) != "XPAKPACK" || string(segment[len(segment)-8:]) != "XPAKSTOP" {
		return nil, true, fmt.Errorf("binpkg: invalid XPAK framing")
	}
	indexSize, dataSize := int(binary.BigEndian.Uint32(segment[8:12])), int(binary.BigEndian.Uint32(segment[12:16]))
	if 16+indexSize+dataSize+8 != len(segment) {
		return nil, true, fmt.Errorf("binpkg: invalid XPAK lengths")
	}
	index, data := segment[16:16+indexSize], segment[16+indexSize:16+indexSize+dataSize]
	result := make(map[string]string)
	for position := 0; position < len(index); {
		if len(index)-position < 12 {
			return nil, true, fmt.Errorf("binpkg: truncated XPAK index")
		}
		nameLength := int(binary.BigEndian.Uint32(index[position : position+4]))
		position += 4
		if nameLength <= 0 || nameLength > len(index)-position-8 {
			return nil, true, fmt.Errorf("binpkg: invalid XPAK name length")
		}
		name := string(index[position : position+nameLength])
		position += nameLength
		offset := int(binary.BigEndian.Uint32(index[position : position+4]))
		length := int(binary.BigEndian.Uint32(index[position+4 : position+8]))
		position += 8
		if filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00") || length > len(data) || offset > len(data)-length {
			return nil, true, fmt.Errorf("binpkg: invalid XPAK index entry")
		}
		if _, exists := result[name]; exists {
			return nil, true, fmt.Errorf("binpkg: duplicate XPAK metadata entry")
		}
		result[name] = strings.TrimSuffix(string(data[offset:offset+length]), "\n")
	}
	return result, true, nil
}

func readLegacyXPAKMetadata(f *os.File, size int64) (map[string]string, error) {
	readSize := int64(xpakTrailerLen)
	if size < readSize {
		readSize = size
	}
	if readSize == 0 {
		return nil, fmt.Errorf("binpkg: the package file is empty")
	}

	buf := make([]byte, readSize)
	if _, err := f.ReadAt(buf, size-readSize); err != nil {
		return nil, fmt.Errorf("binpkg: could not read package file trailer: %w", err)
	}

	idx := bytes.LastIndex(buf, []byte(xpakMagic+"\n"))
	if idx < 0 {
		return nil, fmt.Errorf("binpkg: the package file appears to be corrupted (missing metadata marker)")
	}

	afterMagic := buf[idx+len(xpakMagic)+1:]
	nlIdx := bytes.IndexByte(afterMagic, '\n')
	if nlIdx < 0 {
		return nil, fmt.Errorf("binpkg: the package file appears to be corrupted (invalid metadata format)")
	}

	offsetStr := string(afterMagic[:nlIdx])
	offset, err := strconv.ParseInt(offsetStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("binpkg: the package file metadata is corrupted (invalid offset %q): %w", offsetStr, err)
	}
	if offset > size || offset < 0 {
		return nil, fmt.Errorf("binpkg: the package file metadata offset %d is outside the file (file is %d bytes)", offset, size)
	}
	if offset > maxXPAKMetadataBytes {
		return nil, fmt.Errorf("binpkg: package metadata exceeds the %d byte limit", maxXPAKMetadataBytes)
	}

	metaStart := size - offset

	metaData := make([]byte, offset)
	if _, err := f.ReadAt(metaData, metaStart); err != nil {
		return nil, fmt.Errorf("binpkg: could not read embedded package metadata: %w", err)
	}

	cut := bytes.LastIndex(metaData, []byte("\n"+xpakMagic+"\n"))
	if cut < 0 {
		cut = len(metaData) - len(xpakMagic) - 1 - len(offsetStr) - 1
		if cut < 0 {
			cut = 0
		}
	}
	metaBytes := metaData[:cut]

	return parseMetadataLines(metaBytes), nil
}

func parseMetadataLines(data []byte) map[string]string {
	result := make(map[string]string)
	lines := bytes.Split(data, []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		if idx := bytes.IndexByte(line, '='); idx >= 0 {
			key := string(line[:idx])
			val := string(line[idx+1:])
			result[key] = val
		}
	}
	return result
}

func Extract(ctx context.Context, pkgPath string, destDir string) error {
	return ExtractWithPolicy(ctx, pkgPath, destDir, DefaultExtractionPolicy)
}

// ExtractWithGPKGPolicy applies signature and extraction policy to GPKG
// packages while retaining the normal extraction policy for legacy XPAK.
func ExtractWithGPKGPolicy(ctx context.Context, pkgPath, destDir string, policy GPKGPolicy) error {
	if IsGPKG(pkgPath) {
		return ExtractGPKG(ctx, pkgPath, destDir, policy)
	}
	return ExtractWithPolicy(ctx, pkgPath, destDir, policy.Extraction)
}

func ExtractWithPolicy(ctx context.Context, pkgPath string, destDir string, policy ExtractionPolicy) error {
	if policy.MaxEntries <= 0 || policy.MaxTotalBytes <= 0 || policy.MaxFileBytes <= 0 || policy.MaxXAttrBytes <= 0 ||
		policy.MaxFileBytes > policy.MaxTotalBytes {
		return fmt.Errorf("binpkg: invalid extraction resource policy")
	}
	if IsGPKG(pkgPath) {
		return ExtractGPKG(ctx, pkgPath, destDir, GPKGPolicy{Extraction: policy})
	}
	comp := detectCompression(pkgPath)

	f, err := os.Open(pkgPath)
	if err != nil {
		return fmt.Errorf("binpkg: could not open %s: %w", pkgPath, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil { /* Best effort */
		}
	}()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("binpkg: could not read information for %s: %w", pkgPath, err)
	}

	tarStart, err := findXPAKStart(f, fi.Size())
	if err != nil {
		return fmt.Errorf("binpkg: could not locate the data section in %s: %w", pkgPath, err)
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("binpkg: could not position read cursor in package file: %w", err)
	}

	var reader io.Reader

	switch comp {
	case CompressionBzip2:
		reader = io.LimitReader(f, tarStart)
		reader = bzip2.NewReader(reader)
	case CompressionXz:
		return extractWithXz(ctx, pkgPath, destDir, policy)
	case CompressionNone:
		reader = io.LimitReader(f, tarStart)
	default:
		reader = io.LimitReader(f, tarStart)
		reader = bzip2.NewReader(reader)
	}

	return untar(reader, destDir, policy)
}

func extractWithXz(ctx context.Context, pkgPath, destDir string, policy ExtractionPolicy) error {
	cmd := exec.CommandContext(ctx, "xz", "-d", "-c", pkgPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("binpkg: could not open xz output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("binpkg: could not decompress xz-compressed package: %w", err)
	}
	extractErr := untar(stdout, destDir, policy)
	_, drainErr := io.Copy(io.Discard, io.LimitReader(stdout, policy.MaxTotalBytes+1))
	waitErr := cmd.Wait()
	if extractErr != nil {
		return extractErr
	}
	if drainErr != nil {
		return fmt.Errorf("binpkg: could not drain xz output: %w", drainErr)
	}
	if waitErr != nil {
		return fmt.Errorf("binpkg: could not decompress xz-compressed package: %w", waitErr)
	}
	return nil
}

func findXPAKStart(f *os.File, size int64) (int64, error) {
	if size >= 16 {
		trailer := make([]byte, 16)
		if _, err := f.ReadAt(trailer, size-16); err != nil {
			return 0, err
		}
		if string(trailer[:8]) == "XPAKSTOP" && string(trailer[12:]) == "STOP" {
			segmentSize := int64(binary.BigEndian.Uint32(trailer[8:12]))
			if segmentSize < 24 || segmentSize > maxXPAKMetadataBytes || segmentSize+8 > size {
				return 0, fmt.Errorf("binpkg: invalid XPAK segment size")
			}
			return size - segmentSize - 8, nil
		}
	}
	readSize := int64(xpakTrailerLen)
	if size < readSize {
		readSize = size
	}
	if readSize == 0 {
		return 0, nil
	}

	buf := make([]byte, readSize)
	if _, err := f.ReadAt(buf, size-readSize); err != nil {
		return 0, fmt.Errorf("binpkg: could not read package file trailer: %w", err)
	}

	idx := bytes.LastIndex(buf, []byte(xpakMagic+"\n"))
	if idx < 0 {
		return size, nil
	}

	afterMagic := buf[idx+len(xpakMagic)+1:]
	nlIdx := bytes.IndexByte(afterMagic, '\n')
	if nlIdx < 0 {
		return size, nil
	}

	offsetStr := string(afterMagic[:nlIdx])
	offset, err := strconv.ParseInt(offsetStr, 10, 64)
	if err != nil {
		return size, nil
	}
	if offset > size || offset < 0 {
		return size, nil
	}

	return size - offset, nil
}

func untar(r io.Reader, destDir string, policy ExtractionPolicy) error {
	tr := tar.NewReader(r)
	seen := make(map[string]struct{})
	entries := 0
	var totalBytes int64
	var directories []struct {
		path string
		hdr  tar.Header
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("binpkg: could not read an entry from the package archive: %w", err)
		}
		entries++
		if entries > policy.MaxEntries {
			return fmt.Errorf("binpkg: archive exceeds the %d entry limit", policy.MaxEntries)
		}
		if hdr.Size < 0 || hdr.Size > policy.MaxFileBytes || totalBytes > policy.MaxTotalBytes-hdr.Size {
			return fmt.Errorf("binpkg: archive entry %q exceeds extraction size limits", hdr.Name)
		}
		totalBytes += hdr.Size
		var xattrBytes int64
		for key, value := range hdr.PAXRecords {
			if strings.HasPrefix(key, xattrPAXPrefix) {
				xattrBytes += int64(len(key) + len(value))
			}
		}
		if xattrBytes > policy.MaxXAttrBytes {
			return fmt.Errorf("binpkg: archive entry %q exceeds extended-attribute limits", hdr.Name)
		}

		name, target, err := confinedArchivePath(destDir, hdr.Name)
		if err != nil {
			return fmt.Errorf("binpkg: unsafe archive entry %q: %w", hdr.Name, err)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("binpkg: unsafe archive entry %q: duplicate path", hdr.Name)
		}
		seen[name] = struct{}{}
		if err := rejectSymlinkParents(destDir, target); err != nil {
			return fmt.Errorf("binpkg: unsafe archive entry %q: %w", hdr.Name, err)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := rejectExistingNonDirectory(target); err != nil {
				return fmt.Errorf("binpkg: unsafe archive entry %q: %w", hdr.Name, err)
			}
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0777); err != nil {
				return fmt.Errorf("binpkg: could not create directory %s while extracting: %w", target, err)
			}
			directories = append(directories, struct {
				path string
				hdr  tar.Header
			}{target, *hdr})
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("binpkg: could not create parent directory during extraction: %w", err)
			}
			if err := rejectExistingSymlinkOrDirectory(target); err != nil {
				return fmt.Errorf("binpkg: unsafe archive entry %q: %w", hdr.Name, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0777)
			if err != nil {
				return fmt.Errorf("binpkg: could not create file %s while extracting: %w", target, err)
			}
			if err := copyArchiveFile(f, tr, hdr); err != nil {
				if cerr := f.Close(); cerr != nil { /* cleanup on error */
				}
				return fmt.Errorf("binpkg: failed writing extracted file %s: %w", target, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("binpkg: failed closing extracted file %s: %w", target, err)
			}
			if err := applyArchiveMetadata(target, hdr, false, policy.PreserveOwnership); err != nil {
				return fmt.Errorf("binpkg: restore metadata for %s: %w", target, err)
			}
		case tar.TypeLink:
			_, linkTarget, err := confinedArchivePath(destDir, hdr.Linkname)
			if err != nil {
				return fmt.Errorf("binpkg: unsafe hardlink target %q: %w", hdr.Linkname, err)
			}
			if _, exists := seen[filepath.Clean(filepath.FromSlash(hdr.Linkname))]; !exists {
				return fmt.Errorf("binpkg: unsafe hardlink target %q: target must precede link", hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("binpkg: create hardlink parent: %w", err)
			}
			if err := os.Link(linkTarget, target); err != nil {
				return fmt.Errorf("binpkg: create hardlink %s: %w", target, err)
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("binpkg: could not create parent directory during extraction: %w", err)
			}
			if _, err := os.Lstat(target); err == nil {
				return fmt.Errorf("binpkg: unsafe archive entry %q: target already exists", hdr.Name)
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("binpkg: could not inspect symlink target %s: %w", target, err)
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return fmt.Errorf("binpkg: could not create symlink %s -> %s: %w", target, hdr.Linkname, err)
			}
			if err := applyArchiveMetadata(target, hdr, true, policy.PreserveOwnership); err != nil {
				return fmt.Errorf("binpkg: restore symlink metadata for %s: %w", target, err)
			}
		default:
			return fmt.Errorf("binpkg: unsafe archive entry %q: unsupported type %d", hdr.Name, hdr.Typeflag)
		}
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := applyArchiveMetadata(directories[index].path, &directories[index].hdr, false, policy.PreserveOwnership); err != nil {
			return fmt.Errorf("binpkg: restore directory metadata for %s: %w", directories[index].path, err)
		}
	}

	return nil
}

func copyArchiveFile(file *os.File, reader io.Reader, hdr *tar.Header) error {
	encoded := hdr.PAXRecords[sparsePAXKey]
	if encoded == "" {
		_, err := io.Copy(file, reader)
		return err
	}
	var extents []SparseExtent
	if err := json.Unmarshal([]byte(encoded), &extents); err != nil {
		return fmt.Errorf("invalid sparse extent map: %w", err)
	}
	cursor := int64(0)
	for _, extent := range extents {
		if extent.Offset < cursor || extent.Length < 0 || extent.Offset > hdr.Size ||
			extent.Length > hdr.Size-extent.Offset {
			return fmt.Errorf("invalid sparse extent")
		}
		if _, err := io.CopyN(io.Discard, reader, extent.Offset-cursor); err != nil {
			return err
		}
		if _, err := file.Seek(extent.Offset, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.CopyN(file, reader, extent.Length); err != nil {
			return err
		}
		cursor = extent.Offset + extent.Length
	}
	if _, err := io.CopyN(io.Discard, reader, hdr.Size-cursor); err != nil {
		return err
	}
	return file.Truncate(hdr.Size)
}

func applyArchiveMetadata(path string, hdr *tar.Header, symlink, preserveOwnership bool) error {
	if symlink {
		if preserveOwnership {
			if err := os.Lchown(path, hdr.Uid, hdr.Gid); err != nil {
				return err
			}
		}
	} else {
		if preserveOwnership {
			if err := os.Chown(path, hdr.Uid, hdr.Gid); err != nil {
				return err
			}
		}
		mode := os.FileMode(hdr.Mode) & os.ModePerm
		if hdr.Mode&04000 != 0 {
			mode |= os.ModeSetuid
		}
		if hdr.Mode&02000 != 0 {
			mode |= os.ModeSetgid
		}
		if hdr.Mode&01000 != 0 {
			mode |= os.ModeSticky
		}
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
	}
	if err := applyExtendedAttributes(path, hdr.PAXRecords, symlink); err != nil {
		return err
	}
	atime := hdr.AccessTime
	if atime.IsZero() {
		atime = hdr.ModTime
	}
	if symlink {
		return setSymlinkTimes(path, atime, hdr.ModTime)
	}
	return os.Chtimes(path, atime, hdr.ModTime)
}

func rejectExistingNonDirectory(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not inspect target %s: %w", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("target %s exists and is not a directory", path)
	}
	return nil
}

func rejectExistingSymlinkOrDirectory(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not inspect target %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("target %s exists and is not a regular file", path)
	}
	return nil
}

func confinedArchivePath(destDir, name string) (string, string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", "", fmt.Errorf("path must be relative")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes the extraction root")
	}
	root := filepath.Clean(destDir)
	target := filepath.Join(root, clean)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative != clean {
		return "", "", fmt.Errorf("path escapes the extraction root")
	}
	return clean, target, nil
}

func rejectSymlinkParents(root, target string) error {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return fmt.Errorf("could not resolve extraction path: %w", err)
	}
	current := filepath.Clean(root)
	parts := strings.Split(relative, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return fmt.Errorf("could not inspect parent %s: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("parent %s is a symlink", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("parent %s is not a directory", current)
		}
	}
	return nil
}

func Create(ctx context.Context, vdbEntryPath string, rootDir string, pkgDir string) (string, error) {
	return CreateRecoveryArtifact(ctx, CaptureRequest{
		VDBEntryPath: vdbEntryPath,
		RootDir:      rootDir,
		PackageDir:   pkgDir,
		Provenance:   LegacyCaptureProvenance(),
	})
}

func CreateRecoveryArtifact(ctx context.Context, request CaptureRequest) (string, error) {
	if err := validateCaptureProvenance(request.Provenance); err != nil {
		return "", err
	}
	vdbEntryPath := request.VDBEntryPath
	rootDir := request.RootDir
	pkgDir := request.PackageDir
	meta, err := readVDBMetadata(vdbEntryPath)
	if err != nil {
		return "", fmt.Errorf("binpkg: could not read installed package metadata: %w", err)
	}

	entries, err := parseContents(filepath.Join(vdbEntryPath, "CONTENTS"))
	if err != nil {
		return "", fmt.Errorf("binpkg: could not parse installed file list: %w", err)
	}

	cat := meta["CATEGORY"]
	pkg := meta["PACKAGE"]
	ver := meta["VERSION"]
	for _, field := range []struct{ name, value string }{
		{"CATEGORY", cat}, {"PACKAGE", pkg}, {"VERSION", ver},
	} {
		if err := validatePackagePathComponent(field.value); err != nil {
			return "", fmt.Errorf("binpkg: invalid installed package %s %q: %w", field.name, field.value, err)
		}
	}
	recoveryManifest, recoveryJSON, err := buildRecoveryManifest(meta, vdbEntryPath, rootDir, entries, request.Provenance)
	if err != nil {
		return "", err
	}
	embedRecoveryManifest(meta, recoveryJSON)
	payloadEvidence := make(map[string]FileEvidence, len(recoveryManifest.Payload))
	for _, evidence := range recoveryManifest.Payload {
		payloadEvidence[evidence.Path] = evidence
	}

	outDir := filepath.Join(pkgDir, cat)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", fmt.Errorf("binpkg: failed to create binary package directory %s: %w", outDir, err)
	}

	outPath := filepath.Join(outDir, pkg+"-"+ver+".tbz2")
	if err := rejectSymlinkParents(pkgDir, outPath); err != nil {
		return "", fmt.Errorf("binpkg: unsafe binary package destination: %w", err)
	}

	tmpF, err := os.CreateTemp(outDir, "."+filepath.Base(outPath)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("binpkg: failed to create temporary package file: %w", err)
	}
	tmpPath := tmpF.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(tmpPath)
		}
	}()

	bzWriter := newBzip2Writer(tmpF)
	tw := tar.NewWriter(bzWriter)
	type inodeKey struct {
		dev uint64
		ino uint64
	}
	hardlinks := make(map[inodeKey]string)

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			cleanup(tw, bzWriter, tmpF)
			return "", fmt.Errorf("binpkg: package capture cancelled: %w", err)
		}
		archivePath, srcPath, err := installedContentPath(rootDir, entry.Path)
		if err != nil {
			cleanup(tw, bzWriter, tmpF)
			return "", fmt.Errorf("binpkg: invalid installed path %q: %w", entry.Path, err)
		}
		fi, err := os.Lstat(srcPath)
		if err != nil {
			cleanup(tw, bzWriter, tmpF)
			return "", fmt.Errorf("binpkg: cannot capture installed path %s: %w", entry.Path, err)
		}
		if err := rejectSymlinkParents(rootDir, srcPath); err != nil {
			cleanup(tw, bzWriter, tmpF)
			return "", fmt.Errorf("binpkg: cannot capture installed path %s: %w", entry.Path, err)
		}
		switch entry.Type {
		case "dir":
			if !fi.IsDir() {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: installed path %s changed type: CONTENTS records a directory, found %s", entry.Path, fi.Mode().Type())
			}
			hdr, err := tar.FileInfoHeader(fi, "")
			if err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: cannot describe directory %s: %w", entry.Path, err)
			}
			hdr.Name = archivePath
			if err := addFilesystemMetadata(hdr, srcPath, false); err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: capture directory metadata for %s: %w", entry.Path, err)
			}
			if err := tw.WriteHeader(hdr); err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: failed to write directory entry for %s in package: %w", entry.Path, err)
			}
		case "obj":
			if !fi.Mode().IsRegular() {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: installed path %s changed type: CONTENTS records a regular file, found %s", entry.Path, fi.Mode().Type())
			}
			hdr, err := tar.FileInfoHeader(fi, "")
			if err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: cannot describe file %s: %w", entry.Path, err)
			}
			hdr.Name = archivePath
			if stat, ok := fi.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
				key := inodeKey{dev: uint64(stat.Dev), ino: stat.Ino}
				if first, exists := hardlinks[key]; exists {
					hdr.Typeflag = tar.TypeLink
					hdr.Linkname = first
					hdr.Size = 0
				} else {
					hardlinks[key] = archivePath
				}
			}
			if err := addFilesystemMetadata(hdr, srcPath, false); err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: capture file metadata for %s: %w", entry.Path, err)
			}
			if err := tw.WriteHeader(hdr); err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: failed to write file entry for %s in package: %w", entry.Path, err)
			}
			if hdr.Typeflag == tar.TypeLink {
				continue
			}
			srcF, err := os.Open(srcPath)
			if err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: cannot open file %s to include in package: %w", srcPath, err)
			}
			hash := sha256.New()
			if _, err := io.Copy(io.MultiWriter(tw, hash), srcF); err != nil {
				srcF.Close()
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: failed copying file %s into package: %w", srcPath, err)
			}
			if err := srcF.Close(); err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: failed closing file %s after packaging: %w", srcPath, err)
			}
			captured := hex.EncodeToString(hash.Sum(nil))
			if captured != payloadEvidence[archivePath].SHA256 {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: installed file %s changed while it was being captured", entry.Path)
			}
		case "sym":
			if fi.Mode()&os.ModeSymlink == 0 {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: installed path %s changed type: CONTENTS records a symlink, found %s", entry.Path, fi.Mode().Type())
			}
			link, err := os.Readlink(srcPath)
			if err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: cannot read symlink target at %s: %w", srcPath, err)
			}
			if link != entry.Target {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: installed symlink %s changed target: CONTENTS records %q, found %q", entry.Path, entry.Target, link)
			}
			hdr, err := tar.FileInfoHeader(fi, link)
			if err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: cannot describe symlink %s: %w", entry.Path, err)
			}
			hdr.Name = archivePath
			if err := addFilesystemMetadata(hdr, srcPath, true); err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: capture symlink metadata for %s: %w", entry.Path, err)
			}
			if err := tw.WriteHeader(hdr); err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: could not write symlink header to package: %w", err)
			}
		default:
			cleanup(tw, bzWriter, tmpF)
			return "", fmt.Errorf("binpkg: unsupported CONTENTS entry type %q for %s", entry.Type, entry.Path)
		}
	}

	if err := tw.Close(); err != nil {
		bzWriter.Close()
		tmpF.Close()
		return "", fmt.Errorf("binpkg: could not finalize the package archive: %w", err)
	}
	if err := bzWriter.Close(); err != nil {
		tmpF.Close()
		return "", fmt.Errorf("binpkg: could not finalize the compressed package: %w", err)
	}

	xpakMeta := buildXPAKMetadata(meta)

	if _, err := tmpF.Write(xpakMeta); err != nil {
		tmpF.Close()
		return "", fmt.Errorf("binpkg: could not write package metadata: %w", err)
	}

	if err := tmpF.Sync(); err != nil {
		_ = tmpF.Close()
		return "", fmt.Errorf("binpkg: could not sync the temporary package file: %w", err)
	}
	if err := tmpF.Close(); err != nil {
		return "", fmt.Errorf("binpkg: could not finalize the temporary package file: %w", err)
	}

	if err := os.Rename(tmpPath, outPath); err != nil {
		return "", fmt.Errorf("binpkg: could not save the completed package: %w", err)
	}
	published = true
	outDirectory, err := os.Open(outDir)
	if err != nil {
		return "", fmt.Errorf("binpkg: could not open package directory for sync: %w", err)
	}
	syncErr := outDirectory.Sync()
	closeErr := outDirectory.Close()
	if syncErr != nil {
		return "", fmt.Errorf("binpkg: could not sync package directory: %w", syncErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("binpkg: could not close package directory after sync: %w", closeErr)
	}

	return outPath, nil
}

func addFilesystemMetadata(hdr *tar.Header, path string, symlink bool) error {
	hdr.Format = tar.FormatPAX
	attributes, err := readExtendedAttributes(path, symlink)
	if err != nil {
		return err
	}
	if len(attributes) > 0 && hdr.PAXRecords == nil {
		hdr.PAXRecords = make(map[string]string)
	}
	for name, value := range attributes {
		hdr.PAXRecords[xattrPAXPrefix+name] = value
	}
	if !symlink && hdr.Typeflag == tar.TypeReg {
		extents, err := sparseMap(path, hdr.Size)
		if err != nil {
			return err
		}
		if extents != nil {
			encoded, err := encodeSparseMap(extents)
			if err != nil {
				return err
			}
			if hdr.PAXRecords == nil {
				hdr.PAXRecords = make(map[string]string)
			}
			hdr.PAXRecords[sparsePAXKey] = encoded
		}
	}
	return nil
}

func validatePackagePathComponent(value string) error {
	if value == "" || value == "." || value == ".." {
		return fmt.Errorf("value is empty or reserved")
	}
	if filepath.Base(value) != value || strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("value contains a path separator")
	}
	return nil
}

func installedContentPath(rootDir, recorded string) (string, string, error) {
	if recorded == "" || !filepath.IsAbs(recorded) {
		return "", "", fmt.Errorf("CONTENTS path must be absolute")
	}
	clean := filepath.Clean(recorded)
	relative := strings.TrimPrefix(clean, string(filepath.Separator))
	if relative == "" || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("CONTENTS path escapes ROOT")
	}
	root := filepath.Clean(rootDir)
	target := filepath.Join(root, relative)
	inside, err := filepath.Rel(root, target)
	if err != nil || inside != relative {
		return "", "", fmt.Errorf("CONTENTS path escapes ROOT")
	}
	return filepath.ToSlash(relative), target, nil
}

func buildXPAKMetadata(meta map[string]string) []byte {
	values := make(map[string][]byte)
	for _, key := range []string{
		"CATEGORY", "PF", "PACKAGE", "VERSION", "SLOT", "USE", "EAPI", "BUILD_TIME", "CHOST", "repository",
		"BUILD_ID", "ABI", "CBUILD", "CTARGET",
		recoveryManifestKey, recoveryManifestSHA256Key,
	} {
		if v, ok := meta[key]; ok {
			values[key] = []byte(v + "\n")
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var index, data bytes.Buffer
	for _, key := range keys {
		_ = binary.Write(&index, binary.BigEndian, uint32(len(key)))
		index.WriteString(key)
		_ = binary.Write(&index, binary.BigEndian, uint32(data.Len()))
		_ = binary.Write(&index, binary.BigEndian, uint32(len(values[key])))
		data.Write(values[key])
	}
	var segment bytes.Buffer
	segment.WriteString("XPAKPACK")
	_ = binary.Write(&segment, binary.BigEndian, uint32(index.Len()))
	_ = binary.Write(&segment, binary.BigEndian, uint32(data.Len()))
	segment.Write(index.Bytes())
	segment.Write(data.Bytes())
	segment.WriteString("XPAKSTOP")
	var framed bytes.Buffer
	framed.Write(segment.Bytes())
	_ = binary.Write(&framed, binary.BigEndian, uint32(segment.Len()))
	framed.WriteString("STOP")
	return framed.Bytes()
}

func readVDBMetadata(vdbPath string) (map[string]string, error) {
	meta := make(map[string]string)
	files := []string{
		"CATEGORY", "PF", "SLOT", "USE", "EAPI", "BUILD_TIME", "BUILD_ID",
		"ABI", "CBUILD", "CHOST", "CTARGET", "repository",
	}

	for _, fn := range files {
		data, err := os.ReadFile(filepath.Join(vdbPath, fn))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("binpkg: could not read package metadata file %s: %w", fn, err)
		}
		meta[fn] = strings.TrimSpace(string(data))
	}

	pf := meta["PF"]
	if pf != "" && meta["VERSION"] == "" {
		parsed, err := atom.Parse(meta["CATEGORY"] + "/" + pf)
		if err != nil || parsed.Version == nil {
			return nil, fmt.Errorf("binpkg: invalid installed PF %q", pf)
		}
		meta["PACKAGE"] = parsed.Package
		meta["VERSION"] = parsed.Version.Raw
	}

	return meta, nil
}

type contentEntry struct {
	Type   string
	Path   string
	Target string
	Size   int64
	Mtime  int64
}

func parseContents(path string) ([]contentEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var entries []contentEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		entry, err := parseContentsLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber, err)
		}
		entries = append(entries, *entry)
	}
	return entries, scanner.Err()
}

func parseContentsLine(line string) (*contentEntry, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil, fmt.Errorf("malformed CONTENTS entry")
	}

	entry := &contentEntry{
		Type: fields[0],
		Path: fields[1],
	}

	switch entry.Type {
	case "obj":
		if len(fields) != 4 {
			return nil, fmt.Errorf("malformed obj entry for %s", entry.Path)
		}
		entry.Size, _ = strconv.ParseInt(fields[2], 10, 64)
		var err error
		entry.Mtime, err = strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid obj timestamp for %s: %w", entry.Path, err)
		}
	case "sym":
		idx := strings.Index(line, "->")
		if idx < 0 {
			return nil, fmt.Errorf("malformed sym entry for %s", entry.Path)
		}
		targetAndTime := strings.Fields(strings.TrimSpace(line[idx+2:]))
		if len(targetAndTime) != 2 {
			return nil, fmt.Errorf("malformed sym entry for %s", entry.Path)
		}
		entry.Target = targetAndTime[0]
		var err error
		entry.Mtime, err = strconv.ParseInt(targetAndTime[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid sym timestamp for %s: %w", entry.Path, err)
		}
	case "dir":
		if len(fields) != 2 {
			return nil, fmt.Errorf("malformed dir entry for %s", entry.Path)
		}
	default:
		return nil, fmt.Errorf("unsupported CONTENTS entry type %q", entry.Type)
	}

	return entry, nil
}

func parseSlot(raw string) (slot, subslot string) {
	if idx := strings.IndexByte(raw, '/'); idx >= 0 {
		slot = raw[:idx]
		subslot = raw[idx+1:]
	} else {
		slot = raw
	}
	return slot, subslot
}

func ListAvailable(pkgDir string) ([]*BinPkgInfo, error) {
	var result []*BinPkgInfo

	catDirs, err := os.ReadDir(pkgDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, fmt.Errorf("binpkg: could not read the binary package directory: %w", err)
	}

	for _, catEntry := range catDirs {
		if !catEntry.IsDir() {
			continue
		}
		catName := catEntry.Name()
		if strings.HasPrefix(catName, ".") {
			continue
		}

		catPath := filepath.Join(pkgDir, catName)
		_ = filepath.WalkDir(catPath, func(pkgPath string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				if pkgPath != catPath && strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".tbz2") && !strings.HasSuffix(name, ".txz") &&
				!strings.HasSuffix(name, ".xpak") && !IsGPKG(name) {
				return nil
			}
			binInfo, err := ReadInfo(pkgPath)
			if err == nil {
				result = append(result, binInfo)
			}
			return nil
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Category != result[j].Category {
			return result[i].Category < result[j].Category
		}
		if result[i].Package != result[j].Package {
			return result[i].Package < result[j].Package
		}
		return result[i].Version < result[j].Version
	})

	return result, nil
}

func FindPackage(pkgDir string, atomStr string) (*BinPkgInfo, error) {
	a, err := atom.Parse(atomStr)
	if err != nil {
		return nil, fmt.Errorf("binpkg: could not parse package name %q: %w", atomStr, err)
	}

	pkgs, err := ListAvailable(pkgDir)
	if err != nil {
		return nil, err
	}

	var candidates []*BinPkgInfo
	for _, p := range pkgs {
		if p.Category == a.Category && p.Package == a.Package {
			candidates = append(candidates, p)
		}
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	if a.Version == nil || a.Version.Raw == "" {
		sort.Slice(candidates, func(i, j int) bool {
			vi, _ := atom.ParseVersion(candidates[i].Version)
			vj, _ := atom.ParseVersion(candidates[j].Version)
			return vi.Compare(vj) > 0
		})
		return candidates[0], nil
	}

	av, err := atom.ParseVersion(a.Version.Raw)
	if err != nil {
		return nil, fmt.Errorf("binpkg: could not parse version number %q: %w", a.Version.Raw, err)
	}

	var best *BinPkgInfo
	var bestVer *atom.Version

	for _, c := range candidates {
		cv, err := atom.ParseVersion(c.Version)
		if err != nil {
			continue
		}

		matches := false
		switch a.Op {
		case atom.OpNone, atom.OpEq:
			matches = cv.Compare(av) == 0
		case atom.OpGtEq:
			matches = cv.Compare(av) >= 0
		case atom.OpGt:
			matches = cv.Compare(av) > 0
		case atom.OpLessEq:
			matches = cv.Compare(av) <= 0
		case atom.OpLess:
			matches = cv.Compare(av) < 0
		case atom.OpTilde:
			matches = cv.Compare(av) >= 0 && matchesTilde(cv, av)
		default:
			matches = cv.Compare(av) == 0
		}

		if !matches {
			continue
		}

		if best == nil || cv.Compare(bestVer) > 0 {
			best = c
			bestVer = cv
		}
	}

	return best, nil
}

func matchesTilde(cv, av *atom.Version) bool {
	if len(av.Numbers) == 0 || len(cv.Numbers) == 0 {
		return false
	}
	if cv.Numbers[0] != av.Numbers[0] {
		return false
	}
	if len(av.Numbers) > 1 && (len(cv.Numbers) < 2 || cv.Numbers[1] != av.Numbers[1]) {
		return false
	}
	return true
}

// FindPackageMatchingUse finds a binary package that matches the atom AND
// has USE flags compatible with the current USE configuration.
func FindPackageMatchingUse(pkgDir string, atomStr string, useFlags map[string]bool, respectUse bool) (*BinPkgInfo, error) {
	return FindCompatiblePackage(pkgDir, atomStr, CompatibilityPolicy{
		UseFlags: useFlags, RespectUse: respectUse,
	})
}

type CompatibilityPolicy struct {
	UseFlags   map[string]bool
	IUse       string
	RespectUse bool
	CHOST      string
	ABI        string
	Repository string
	Slot       string
	Subslot    string
}

func FindCompatiblePackage(pkgDir string, atomStr string, policy CompatibilityPolicy) (*BinPkgInfo, error) {
	a, err := atom.Parse(atomStr)
	if err != nil {
		return nil, fmt.Errorf("binpkg: could not parse package name %q: %w", atomStr, err)
	}

	pkgs, err := ListAvailable(pkgDir)
	if err != nil {
		return nil, err
	}

	type candidate struct {
		pkg   *BinPkgInfo
		ver   *atom.Version
		useOk bool
	}

	var candidates []candidate
	for _, p := range pkgs {
		if p.Category != a.Category || p.Package != a.Package {
			continue
		}

		cv, err := atom.ParseVersion(p.Version)
		if err != nil {
			continue
		}

		matches := false
		if a.Version == nil || a.Version.Raw == "" {
			matches = true
		} else {
			av, err := atom.ParseVersion(a.Version.Raw)
			if err != nil {
				continue
			}
			cmp := cv.Compare(av)
			switch a.Op {
			case atom.OpNone, atom.OpEq:
				matches = cmp == 0
			case atom.OpGtEq:
				matches = cmp >= 0
			case atom.OpGt:
				matches = cmp > 0
			case atom.OpLessEq:
				matches = cmp <= 0
			case atom.OpLess:
				matches = cmp < 0
			case atom.OpTilde:
				matches = cmp >= 0 && matchesTilde(cv, av)
			default:
				matches = cmp == 0
			}
		}

		if !matches {
			continue
		}

		if policy.CHOST != "" && p.CHOST != "" && policy.CHOST != p.CHOST {
			continue
		}
		if policy.ABI != "" && p.ABI != "" && policy.ABI != p.ABI {
			continue
		}
		if policy.Repository != "" && p.Repository != "" && policy.Repository != p.Repository {
			continue
		}
		if policy.Slot != "" && p.Slot != "" && policy.Slot != p.Slot {
			continue
		}
		if policy.Subslot != "" && p.Subslot != "" && policy.Subslot != p.Subslot {
			continue
		}
		binUse := parseUseFlags(p.Use)
		useOk := useFlagsCompatibleInDomain(binUse, policy.UseFlags, policy.IUse)

		candidates = append(candidates, candidate{
			pkg:   p,
			ver:   cv,
			useOk: useOk,
		})
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ver != nil && candidates[j].ver != nil {
			return candidates[i].ver.Compare(candidates[j].ver) > 0
		}
		return false
	})

	if policy.RespectUse {
		for _, c := range candidates {
			if c.useOk {
				return c.pkg, nil
			}
		}
		return nil, nil
	}

	return candidates[0].pkg, nil
}

func useFlagsCompatibleInDomain(binary, selected map[string]bool, iuse string) bool {
	domain := strings.Fields(iuse)
	if len(domain) == 0 {
		return useFlagsCompatible(binary, selected)
	}
	for _, token := range domain {
		flag := strings.TrimLeft(token, "+-")
		if flag == "" {
			continue
		}
		if binary[flag] != selected[flag] {
			return false
		}
	}
	return true
}

func parseUseFlags(useStr string) map[string]bool {
	flags := make(map[string]bool)
	if useStr == "" {
		return flags
	}
	for _, f := range strings.Fields(useStr) {
		if strings.HasPrefix(f, "-") {
			flags[f[1:]] = false
		} else {
			flags[f] = true
		}
	}
	return flags
}

func useFlagsCompatible(binUse, configUse map[string]bool) bool {
	for flag, wantEnabled := range configUse {
		gotEnabled, ok := binUse[flag]
		if !ok {
			continue
		}
		if gotEnabled != wantEnabled {
			return false
		}
	}
	return true
}

// DownloadFromBinhost downloads binary packages from a remote HTTP server.
func DownloadFromBinhost(ctx context.Context, binhostURL string, atomStrs []string, destDir string) ([]string, error) {
	return downloadFromBinhost(ctx, &http.Client{Timeout: 120 * time.Second}, binhostURL, atomStrs, destDir)
}

func downloadFromBinhost(ctx context.Context, httpClient *http.Client, binhostURL string, atomStrs []string, destDir string) ([]string, error) {
	if len(atomStrs) == 0 {
		return nil, nil
	}
	validTargets := make([]string, 0, len(atomStrs))
	for _, atomText := range atomStrs {
		if _, err := atom.Parse(atomText); err == nil {
			validTargets = append(validTargets, atomText)
		}
	}
	if len(validTargets) == 0 {
		return nil, nil
	}
	indexRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(binhostURL, "/")+"/Packages", nil)
	if err != nil {
		return nil, fmt.Errorf("binpkg: prepare Packages request: %w", err)
	}
	indexResponse, err := httpClient.Do(indexRequest)
	if err != nil {
		return nil, fmt.Errorf("binpkg: download Packages index: %w", err)
	}
	if indexResponse.StatusCode == http.StatusNotFound {
		_ = indexResponse.Body.Close()
		return nil, fmt.Errorf("binpkg: binhost has no Packages index; refusing unverifiable legacy download")
	}
	if indexResponse.StatusCode < 200 || indexResponse.StatusCode >= 300 {
		_ = indexResponse.Body.Close()
		return nil, fmt.Errorf("binpkg: Packages index returned HTTP %d", indexResponse.StatusCode)
	}
	index, parseErr := ParsePackagesIndex(indexResponse.Body)
	closeErr := indexResponse.Body.Close()
	if parseErr != nil {
		return nil, parseErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("binpkg: close Packages response: %w", closeErr)
	}
	var selected []PackageIndexEntry
	seenPaths := make(map[string]struct{})
	for _, atomText := range validTargets {
		requested, err := atom.Parse(atomText)
		if err != nil {
			return nil, fmt.Errorf("binpkg: invalid requested atom %q: %w", atomText, err)
		}
		var matches []PackageIndexEntry
		var bestVersion *atom.Version
		for _, entry := range index.Packages {
			candidate, err := atom.Parse(entry["CPV"])
			if err != nil || candidate.Version == nil || candidate.Category != requested.Category ||
				candidate.Package != requested.Package || !atomVersionMatches(requested, candidate.Version) {
				continue
			}
			if bestVersion == nil || candidate.Version.Compare(bestVersion) > 0 {
				bestVersion = candidate.Version
				matches = matches[:0]
			}
			if candidate.Version.Compare(bestVersion) == 0 {
				matches = append(matches, entry)
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("binpkg: binhost has no candidate for %s", atomText)
		}
		entry, err := SelectPackageInstance(matches, matches[0]["CPV"], "")
		if err != nil {
			return nil, err
		}
		if _, exists := seenPaths[entry["PATH"]]; !exists {
			selected = append(selected, entry)
			seenPaths[entry["PATH"]] = struct{}{}
		}
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("binpkg: create binpkg destination: %w", err)
	}
	var downloaded []string
	for _, entry := range selected {
		path, err := downloadIndexedPackage(ctx, httpClient, binhostURL, destDir, entry)
		if err != nil {
			return downloaded, err
		}
		downloaded = append(downloaded, path)
	}
	return downloaded, nil
}

func atomVersionMatches(requested *atom.Atom, candidate *atom.Version) bool {
	if requested.Version == nil {
		return true
	}
	comparison := candidate.Compare(requested.Version)
	switch requested.Op {
	case atom.OpGt:
		return comparison > 0
	case atom.OpGtEq:
		return comparison >= 0
	case atom.OpLess:
		return comparison < 0
	case atom.OpLessEq:
		return comparison <= 0
	case atom.OpTilde:
		return matchesTilde(candidate, requested.Version)
	default:
		return comparison == 0
	}
}

func downloadIndexedPackage(ctx context.Context, client *http.Client, binhostURL, destinationRoot string, entry PackageIndexEntry) (string, error) {
	relative := filepath.FromSlash(entry["PATH"])
	destination := filepath.Join(destinationRoot, relative)
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(binhostURL, "/")+"/"+entry["PATH"], nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("binpkg: package %s returned HTTP %d", entry["PATH"], response.StatusCode)
	}
	expectedSize, sizeErr := strconv.ParseInt(entry["SIZE"], 10, 64)
	if sizeErr != nil || expectedSize <= 0 {
		return "", fmt.Errorf("binpkg: Packages entry for %s has invalid SIZE", entry["PATH"])
	}
	if entry["SHA512"] == "" && entry["BLAKE2B"] == "" {
		return "", fmt.Errorf("binpkg: Packages entry for %s has no supported digest", entry["PATH"])
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".part-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	shaHash := sha512.New()
	blakeHash, _ := blake2b.New512(nil)
	written, copyErr := io.Copy(io.MultiWriter(temporary, shaHash, blakeHash), io.LimitReader(response.Body, maxGPKGContainerMember+1))
	if copyErr != nil {
		_ = temporary.Close()
		return "", copyErr
	}
	if written != expectedSize {
		_ = temporary.Close()
		return "", fmt.Errorf("binpkg: downloaded size mismatch for %s", entry["PATH"])
	}
	if digest := entry["SHA512"]; digest != "" && !strings.EqualFold(digest, hex.EncodeToString(shaHash.Sum(nil))) {
		_ = temporary.Close()
		return "", fmt.Errorf("binpkg: downloaded SHA512 mismatch for %s", entry["PATH"])
	}
	if digest := entry["BLAKE2B"]; digest != "" && !strings.EqualFold(digest, hex.EncodeToString(blakeHash.Sum(nil))) {
		_ = temporary.Close()
		return "", fmt.Errorf("binpkg: downloaded BLAKE2B mismatch for %s", entry["PATH"])
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	var validateErr error
	if IsGPKG(entry["PATH"]) {
		_, validateErr = ReadGPKG(ctx, temporaryPath)
	} else {
		_, validateErr = ReadInfo(temporaryPath)
	}
	if validateErr != nil {
		return "", fmt.Errorf("binpkg: downloaded package validation failed: %w", validateErr)
	}
	if err := os.Chmod(temporaryPath, 0644); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return "", err
	}
	published = true
	if err := syncBinpkgDirectory(filepath.Dir(destination)); err != nil {
		return "", err
	}
	return destination, nil
}

func cleanup(closers ...io.Closer) {
	for _, c := range closers {
		if err := c.Close(); err != nil {
			// Best-effort cleanup; original error takes precedence
		}
	}
}

func newBzip2Writer(w io.Writer) io.WriteCloser {
	cmd := exec.Command("bzip2", "-c")
	cmd.Stdout = w
	cmd.Stderr = nil

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return &brokenWriter{err: fmt.Errorf("binpkg: could not set up bzip2 compression: %w", err)}
	}
	if err := cmd.Start(); err != nil {
		return &brokenWriter{err: fmt.Errorf("binpkg: could not start bzip2 compression: %w", err)}
	}

	return &bzip2WriterCmd{cmd: cmd, stdin: stdin}
}

type bzip2WriterCmd struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

func (w *bzip2WriterCmd) Write(p []byte) (int, error) {
	return w.stdin.Write(p)
}

func (w *bzip2WriterCmd) Close() error {
	err := w.stdin.Close()
	waitErr := w.cmd.Wait()
	if err != nil {
		return err
	}
	return waitErr
}

type brokenWriter struct {
	err error
}

func (w *brokenWriter) Write(p []byte) (int, error) {
	return 0, w.err
}

func (w *brokenWriter) Close() error {
	return w.err
}
