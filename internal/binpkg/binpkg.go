package binpkg

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/bzip2"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/atom"
)

const xpakMagic = "XPAKSTOP"
const xpakTrailerLen = 4096

type BinPkgInfo struct {
	Category  string
	Package   string
	Version   string
	Slot      string
	Subslot   string
	Use       string
	EAPI      string
	BuildTime int64
	Size      int64
	Path      string
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
		return extractWithXz(ctx, pkgPath, destDir, tarStart)
	case CompressionNone:
		reader = io.LimitReader(f, tarStart)
	default:
		reader = io.LimitReader(f, tarStart)
		reader = bzip2.NewReader(reader)
	}

	return untar(reader, destDir)
}

func extractWithXz(ctx context.Context, pkgPath, destDir string, tarStart int64) error {
	cmd := exec.CommandContext(ctx, "xz", "-d", "-c", pkgPath)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("binpkg: could not decompress xz-compressed package: %w", err)
	}

	data := stdout.Bytes()
	if tarStart > 0 && tarStart < int64(len(data)) {
		data = data[:tarStart]
	}

	return untar(bytes.NewReader(data), destDir)
}

func findXPAKStart(f *os.File, size int64) (int64, error) {
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

func untar(r io.Reader, destDir string) error {
	tr := tar.NewReader(r)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("binpkg: could not read an entry from the package archive: %w", err)
		}

		target := filepath.Join(destDir, hdr.Name)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0777); err != nil {
				return fmt.Errorf("binpkg: could not create directory %s while extracting: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("binpkg: could not create parent directory during extraction: %w", err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0777)
			if err != nil {
				return fmt.Errorf("binpkg: could not create file %s while extracting: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				if cerr := f.Close(); cerr != nil { /* cleanup on error */
				}
				return fmt.Errorf("binpkg: failed writing extracted file %s: %w", target, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("binpkg: failed closing extracted file %s: %w", target, err)
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("binpkg: could not create parent directory during extraction: %w", err)
			}
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("binpkg: could not remove %s before creating symlink: %w", target, err)
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return fmt.Errorf("binpkg: could not create symlink %s -> %s: %w", target, hdr.Linkname, err)
			}
		}
	}

	return nil
}

func Create(ctx context.Context, vdbEntryPath string, rootDir string, pkgDir string) (string, error) {
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

	outDir := filepath.Join(pkgDir, cat)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", fmt.Errorf("binpkg: failed to create binary package directory %s: %w", outDir, err)
	}

	outPath := filepath.Join(outDir, pkg+"-"+ver+".tbz2")

	tmpPath := outPath + ".tmp"
	tmpF, err := os.Create(tmpPath)
	if err != nil {
		return "", fmt.Errorf("binpkg: failed to create temporary package file: %w", err)
	}

	bzWriter := newBzip2Writer(tmpF)
	tw := tar.NewWriter(bzWriter)

	for _, entry := range entries {
		srcPath := filepath.Join(rootDir, entry.Path)
		switch entry.Type {
		case "dir":
			hdr := &tar.Header{
				Name:     entry.Path,
				Typeflag: tar.TypeDir,
				Mode:     0755,
			}
			if err := tw.WriteHeader(hdr); err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: failed to write directory entry for %s in package: %w", entry.Path, err)
			}
		case "obj":
			fi, err := os.Lstat(srcPath)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: cannot read file %s to include in package: %w", srcPath, err)
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				link, err := os.Readlink(srcPath)
				if err != nil {
					cleanup(tw, bzWriter, tmpF)
					return "", fmt.Errorf("binpkg: cannot read symlink target at %s: %w", srcPath, err)
				}
				hdr := &tar.Header{
					Name:     entry.Path,
					Typeflag: tar.TypeSymlink,
					Linkname: link,
					Mode:     int64(fi.Mode() & os.ModePerm),
				}
				if err := tw.WriteHeader(hdr); err != nil {
					cleanup(tw, bzWriter, tmpF)
					return "", fmt.Errorf("binpkg: failed to write symlink entry for %s in package: %w", entry.Path, err)
				}
				continue
			}
			hdr := &tar.Header{
				Name: entry.Path,
				Size: fi.Size(),
				Mode: int64(fi.Mode() & os.ModePerm),
			}
			if err := tw.WriteHeader(hdr); err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: failed to write file entry for %s in package: %w", entry.Path, err)
			}
			srcF, err := os.Open(srcPath)
			if err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: cannot open file %s to include in package: %w", srcPath, err)
			}
			if _, err := io.Copy(tw, srcF); err != nil {
				srcF.Close()
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: failed copying file %s into package: %w", srcPath, err)
			}
			if err := srcF.Close(); err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: failed closing file %s after packaging: %w", srcPath, err)
			}
		case "sym":
			link := entry.Target
			hdr := &tar.Header{
				Name:     entry.Path,
				Typeflag: tar.TypeSymlink,
				Linkname: link,
				Mode:     0777,
			}
			if err := tw.WriteHeader(hdr); err != nil {
				cleanup(tw, bzWriter, tmpF)
				return "", fmt.Errorf("binpkg: could not write symlink header to package: %w", err)
			}
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

	offset := len(xpakMeta) + len(xpakMagic) + 1
	offsetStr := strconv.Itoa(offset + len(strconv.Itoa(offset)) + 1)

	for {
		testOffset := len(xpakMeta) + len(xpakMagic) + 1 + len(offsetStr) + 1
		newOffsetStr := strconv.Itoa(testOffset)
		if newOffsetStr == offsetStr {
			offset = testOffset
			offsetStr = newOffsetStr
			break
		}
		offsetStr = newOffsetStr
		offset = testOffset
	}

	trailer := xpakMagic + "\n" + offsetStr + "\n"
	if _, err := tmpF.WriteString(trailer); err != nil {
		if cerr := tmpF.Close(); cerr != nil { /* Best effort */
		}
		return "", fmt.Errorf("binpkg: could not write package footer: %w", err)
	}

	if err := tmpF.Close(); err != nil {
		return "", fmt.Errorf("binpkg: could not finalize the temporary package file: %w", err)
	}

	if err := os.Rename(tmpPath, outPath); err != nil {
		return "", fmt.Errorf("binpkg: could not save the completed package: %w", err)
	}

	return outPath, nil
}

func buildXPAKMetadata(meta map[string]string) []byte {
	var buf bytes.Buffer
	for _, key := range []string{"CATEGORY", "PF", "PACKAGE", "VERSION", "SLOT", "USE", "EAPI", "BUILD_TIME", "CHOST", "repository"} {
		if v, ok := meta[key]; ok {
			buf.WriteString(key)
			buf.WriteByte('=')
			buf.WriteString(v)
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

func readVDBMetadata(vdbPath string) (map[string]string, error) {
	meta := make(map[string]string)
	files := []string{"CATEGORY", "PF", "SLOT", "USE", "EAPI", "BUILD_TIME", "CHOST", "repository"}

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
		if dash := strings.LastIndexByte(pf, '-'); dash >= 0 {
			meta["PACKAGE"] = pf[:dash]
			meta["VERSION"] = pf[dash+1:]
		}
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
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		entry := parseContentsLine(line)
		if entry == nil {
			continue
		}
		entries = append(entries, *entry)
	}
	return entries, scanner.Err()
}

func parseContentsLine(line string) *contentEntry {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil
	}

	entry := &contentEntry{
		Type: fields[0],
		Path: fields[1],
	}

	switch entry.Type {
	case "obj":
		if len(fields) > 2 {
			entry.Size, _ = strconv.ParseInt(fields[2], 10, 64)
		}
		if len(fields) > 3 {
			entry.Mtime, _ = strconv.ParseInt(fields[3], 10, 64)
		}
	case "sym":
		idx := strings.Index(line, "->")
		if idx >= 0 {
			entry.Target = strings.TrimSpace(line[idx+2:])
			s := strings.Fields(entry.Target)
			if len(s) > 1 {
				entry.Target = s[0]
			}
		}
	case "dir":
	}

	return entry
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
		pkgFiles, err := os.ReadDir(catPath)
		if err != nil {
			continue
		}

		for _, pkgEntry := range pkgFiles {
			name := pkgEntry.Name()
			if !strings.HasSuffix(name, ".tbz2") && !strings.HasSuffix(name, ".txz") && !strings.HasSuffix(name, ".xpak") {
				continue
			}
			info, err := pkgEntry.Info()
			if err != nil {
				continue
			}
			if info.IsDir() {
				continue
			}

			pkgPath := filepath.Join(catPath, name)

			binInfo, err := ReadInfo(pkgPath)
			if err != nil {
				continue
			}
			result = append(result, binInfo)
		}
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

		binUse := parseUseFlags(p.Use)
		useOk := useFlagsCompatible(binUse, useFlags)

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

	if respectUse {
		for _, c := range candidates {
			if c.useOk {
				return c.pkg, nil
			}
		}
		return nil, nil
	}

	return candidates[0].pkg, nil
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
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("binpkg: could not create download destination directory: %w", err)
	}

	var downloaded []string

	for _, atomStr := range atomStrs {
		a, err := atom.Parse(atomStr)
		if err != nil {
			continue
		}

		catDir := filepath.Join(destDir, a.Category)
		if err := os.MkdirAll(catDir, 0755); err != nil {
			return downloaded, fmt.Errorf("binpkg: could not create download directory for category %s: %w", catDir, err)
		}

		version := ""
		if a.Version != nil {
			version = a.Version.Raw
		}

		var pkgName string
		if version != "" {
			pkgName = a.Package + "-" + version + ".tbz2"
		} else {
			pkgName = a.Package + ".tbz2"
		}

		url := strings.TrimRight(binhostURL, "/") + "/" + a.Category + "/" + pkgName
		destPath := filepath.Join(catDir, pkgName)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return downloaded, fmt.Errorf("binpkg: could not prepare download request for %s: %w", url, err)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			return downloaded, fmt.Errorf("binpkg: could not download from %s: %w", url, err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return downloaded, fmt.Errorf("binpkg: server returned error %d when downloading %s", resp.StatusCode, url)
		}

		tmpPath := destPath + ".part"
		fh, err := os.Create(tmpPath)
		if err != nil {
			resp.Body.Close()
			return downloaded, fmt.Errorf("binpkg: could not create temporary download file: %w", err)
		}

		if _, err := io.Copy(fh, resp.Body); err != nil {
			if cerr := fh.Close(); cerr != nil { /* cleanup on error */
			}
			if cerr := resp.Body.Close(); cerr != nil { /* cleanup on error */
			}
			if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) { /* cleanup on error */
			}
			return downloaded, fmt.Errorf("binpkg: download from %s failed: %w", url, err)
		}
		if err := fh.Close(); err != nil {
			resp.Body.Close()
			return downloaded, fmt.Errorf("binpkg: could not finalize downloaded file: %w", err)
		}
		if err := resp.Body.Close(); err != nil {
			return downloaded, fmt.Errorf("binpkg: could not close network connection: %w", err)
		}

		if err := os.Rename(tmpPath, destPath); err != nil {
			if rerr := os.Remove(tmpPath); rerr != nil && !os.IsNotExist(rerr) { /* cleanup on error */
			}
			return downloaded, fmt.Errorf("binpkg: could not save downloaded file: %w", err)
		}

		downloaded = append(downloaded, destPath)
	}

	return downloaded, nil
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
