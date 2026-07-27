package binpkg

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/crypto/blake2b"
)

const (
	GPKGVersion            = "gpkg-1"
	maxGPKGContainerMember = 64 << 30
	maxGPKGMetadataBytes   = 64 << 20
	maxGPKGManifestBytes   = 16 << 20
)

type GPKGSignatureVerifier func(context.Context, []byte) ([]byte, error)

type GPKGPolicy struct {
	RequireSignature bool
	VerifyManifest   GPKGSignatureVerifier
	Extraction       ExtractionPolicy
}

func GPGVManifestVerifier(keyring string) GPKGSignatureVerifier {
	return func(ctx context.Context, signed []byte) ([]byte, error) {
		if keyring == "" || !filepath.IsAbs(keyring) {
			return nil, fmt.Errorf("trusted GPG keyring path must be absolute")
		}
		info, err := os.Stat(keyring)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("trusted GPG keyring is unavailable")
		}
		command := exec.CommandContext(ctx, "gpgv", "--keyring", keyring, "-")
		command.Stdin = bytes.NewReader(signed)
		if output, err := command.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("gpgv rejected Manifest: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return extractClearSignedPayload(signed)
	}
}

type GPKG struct {
	Path       string
	Prefix     string
	Metadata   map[string][]byte
	ImageName  string
	ImageCodec string
	Signed     bool
}

type GPKGCreateRequest struct {
	Path         string
	Basename     string
	ImageRoot    string
	Metadata     map[string][]byte
	ModTime      time.Time
	SignManifest func(context.Context, []byte) ([]byte, error)
}

func CreateInstalledGPKG(ctx context.Context, vdbEntryPath, rootDir, packageDir string) (string, error) {
	meta, err := readVDBMetadata(vdbEntryPath)
	if err != nil {
		return "", err
	}
	entries, err := parseContents(filepath.Join(vdbEntryPath, "CONTENTS"))
	if err != nil {
		return "", fmt.Errorf("binpkg: parse installed CONTENTS for GPKG: %w", err)
	}
	category, pf, packageName := meta["CATEGORY"], meta["PF"], meta["PACKAGE"]
	if err := validatePackagePathComponent(category); err != nil {
		return "", err
	}
	if err := validatePackagePathComponent(pf); err != nil {
		return "", err
	}
	if err := validatePackagePathComponent(packageName); err != nil {
		return "", err
	}
	metadata := make(map[string][]byte)
	vdbFiles, err := os.ReadDir(vdbEntryPath)
	if err != nil {
		return "", err
	}
	for _, entry := range vdbFiles {
		if entry.IsDir() {
			continue
		}
		if filepath.Base(entry.Name()) != entry.Name() {
			return "", fmt.Errorf("binpkg: invalid VDB metadata name %q", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(vdbEntryPath, entry.Name()))
		if err != nil {
			return "", err
		}
		metadata[entry.Name()] = data
	}
	image, err := buildInstalledGPKGImageArchive(ctx, rootDir, entries)
	if err != nil {
		return "", err
	}
	outDir := filepath.Join(packageDir, category, packageName)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", err
	}
	basename := pf
	if meta["BUILD_ID"] != "" {
		basename += "-" + meta["BUILD_ID"]
	}
	outPath := filepath.Join(outDir, basename+".gpkg.tar")
	return outPath, createGPKGFromImageArchive(ctx, GPKGCreateRequest{
		Path: outPath, Basename: basename, Metadata: metadata, ModTime: time.Unix(0, 0).UTC(),
	}, image)
}

type gpkgManifestRecord struct {
	Name    string
	Size    int64
	SHA512  string
	BLAKE2B string
}

type gpkgMember struct {
	Name    string
	Base    string
	Size    int64
	SHA512  string
	BLAKE2B string
}

func IsGPKG(path string) bool {
	return strings.HasSuffix(path, ".gpkg.tar")
}

func ReadGPKG(ctx context.Context, path string) (*GPKG, error) {
	return ReadGPKGWithPolicy(ctx, path, GPKGPolicy{Extraction: DefaultExtractionPolicy})
}

func ReadMetadata(ctx context.Context, path string) (map[string][]byte, error) {
	if IsGPKG(path) {
		pkg, err := ReadGPKG(ctx, path)
		if err != nil {
			return nil, err
		}
		return pkg.Metadata, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	values, err := readXPAKMetadata(file, info.Size())
	if err != nil {
		return nil, err
	}
	result := make(map[string][]byte, len(values))
	for key, value := range values {
		result[key] = []byte(value)
	}
	return result, nil
}

func ReadGPKGWithPolicy(ctx context.Context, path string, policy GPKGPolicy) (*GPKG, error) {
	if policy.Extraction.MaxEntries == 0 {
		policy.Extraction = DefaultExtractionPolicy
	}
	members, prefix, manifest, signed, err := inspectGPKG(ctx, path, policy)
	if err != nil {
		return nil, err
	}
	var metadataName, imageName, imageCodec string
	for _, member := range members {
		base, codec, ok := parseGPKGInnerName(member.Base)
		if !ok {
			continue
		}
		switch base {
		case "metadata":
			if metadataName != "" {
				return nil, fmt.Errorf("binpkg: GPKG contains multiple metadata archives")
			}
			metadataName = member.Name
		case "image":
			if imageName != "" {
				return nil, fmt.Errorf("binpkg: GPKG contains multiple image archives")
			}
			imageName, imageCodec = member.Name, codec
		}
	}
	if metadataName == "" || imageName == "" {
		return nil, fmt.Errorf("binpkg: GPKG is missing its metadata or image archive")
	}
	metadataBlob, codec, err := readGPKGMember(path, metadataName, maxGPKGMetadataBytes)
	if err != nil {
		return nil, err
	}
	metadataReader, closeReader, err := decodeGPKGReader(ctx, bytes.NewReader(metadataBlob), codec)
	if err != nil {
		return nil, err
	}
	metadata, metadataErr := readGPKGMetadata(metadataReader)
	closeErr := closeReader()
	if metadataErr != nil {
		return nil, metadataErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	_ = manifest
	return &GPKG{
		Path: path, Prefix: prefix, Metadata: metadata,
		ImageName: imageName, ImageCodec: imageCodec, Signed: signed,
	}, nil
}

func ExtractGPKG(ctx context.Context, path, destination string, policy GPKGPolicy) error {
	pkg, err := ReadGPKGWithPolicy(ctx, path, policy)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("binpkg: open GPKG: %w", err)
	}
	defer file.Close()
	tr := tar.NewReader(file)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("binpkg: GPKG image archive disappeared")
		}
		if err != nil {
			return fmt.Errorf("binpkg: read GPKG container: %w", err)
		}
		if hdr.Name != pkg.ImageName {
			continue
		}
		reader, closeReader, err := decodeGPKGReader(ctx, io.LimitReader(tr, hdr.Size), pkg.ImageCodec)
		if err != nil {
			return err
		}
		prefix := "image"
		extractErr := untarPrefixed(reader, destination, policy.Extraction, prefix)
		closeErr := closeReader()
		if extractErr != nil {
			return extractErr
		}
		return closeErr
	}
}

func inspectGPKG(ctx context.Context, path string, policy GPKGPolicy) ([]gpkgMember, string, map[string]gpkgManifestRecord, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", nil, false, fmt.Errorf("binpkg: open GPKG: %w", err)
	}
	defer file.Close()
	tr := tar.NewReader(file)
	seen := make(map[string]struct{})
	var members []gpkgMember
	var prefix string
	var manifestData []byte
	versionSeen := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", nil, false, err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", nil, false, fmt.Errorf("binpkg: read GPKG container: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return nil, "", nil, false, fmt.Errorf("binpkg: GPKG outer member %q is not a regular file", hdr.Name)
		}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(hdr.Name)))
		parts := strings.Split(clean, "/")
		if clean != hdr.Name || len(parts) != 2 || parts[0] == "." || parts[0] == ".." || parts[1] == "" {
			return nil, "", nil, false, fmt.Errorf("binpkg: invalid GPKG outer path %q", hdr.Name)
		}
		if prefix == "" {
			prefix = parts[0]
		} else if prefix != parts[0] {
			return nil, "", nil, false, fmt.Errorf("binpkg: GPKG outer members do not share one prefix")
		}
		if _, exists := seen[hdr.Name]; exists {
			return nil, "", nil, false, fmt.Errorf("binpkg: duplicate GPKG outer member %q", hdr.Name)
		}
		seen[hdr.Name] = struct{}{}
		if hdr.Size < 0 || hdr.Size > maxGPKGContainerMember {
			return nil, "", nil, false, fmt.Errorf("binpkg: GPKG outer member %q exceeds limits", hdr.Name)
		}
		shaHash := sha512.New()
		blakeHash, _ := blake2b.New512(nil)
		var capture bytes.Buffer
		writer := io.MultiWriter(shaHash, blakeHash)
		if parts[1] == "Manifest" {
			if hdr.Size > maxGPKGManifestBytes {
				return nil, "", nil, false, fmt.Errorf("binpkg: GPKG Manifest exceeds limits")
			}
			writer = io.MultiWriter(shaHash, blakeHash, &capture)
		}
		if _, err := io.CopyN(writer, tr, hdr.Size); err != nil {
			return nil, "", nil, false, fmt.Errorf("binpkg: read GPKG member %q: %w", hdr.Name, err)
		}
		if parts[1] == GPKGVersion {
			versionSeen = true
		}
		if parts[1] == "Manifest" {
			manifestData = capture.Bytes()
			continue
		}
		members = append(members, gpkgMember{
			Name: hdr.Name, Base: parts[1], Size: hdr.Size,
			SHA512:  hex.EncodeToString(shaHash.Sum(nil)),
			BLAKE2B: hex.EncodeToString(blakeHash.Sum(nil)),
		})
	}
	if !versionSeen || manifestData == nil {
		return nil, "", nil, false, fmt.Errorf("binpkg: GPKG lacks gpkg-1 or Manifest")
	}
	plainManifest, signed, err := verifyGPKGManifest(ctx, manifestData, policy)
	if err != nil {
		return nil, "", nil, false, err
	}
	records, err := parseGPKGManifest(plainManifest)
	if err != nil {
		return nil, "", nil, false, err
	}
	for _, member := range members {
		record, exists := records[member.Base]
		if !exists || record.Size != member.Size ||
			(record.SHA512 == "" && record.BLAKE2B == "") ||
			(record.SHA512 != "" && !strings.EqualFold(record.SHA512, member.SHA512)) ||
			(record.BLAKE2B != "" && !strings.EqualFold(record.BLAKE2B, member.BLAKE2B)) {
			return nil, "", nil, false, fmt.Errorf("binpkg: GPKG Manifest mismatch for %s", member.Base)
		}
		delete(records, member.Base)
	}
	if len(records) != 0 {
		return nil, "", nil, false, fmt.Errorf("binpkg: GPKG Manifest references missing members")
	}
	return members, prefix, records, signed, nil
}

func verifyGPKGManifest(ctx context.Context, data []byte, policy GPKGPolicy) ([]byte, bool, error) {
	signed := bytes.Contains(data, []byte("-----BEGIN PGP SIGNED MESSAGE-----"))
	if !signed {
		if policy.RequireSignature {
			return nil, false, fmt.Errorf("binpkg: GPKG signature is required")
		}
		return data, false, nil
	}
	if policy.VerifyManifest == nil {
		if policy.RequireSignature {
			return nil, true, fmt.Errorf("binpkg: no GPKG signature verifier is configured")
		}
		plain, err := extractClearSignedPayload(data)
		return plain, true, err
	}
	plain, err := policy.VerifyManifest(ctx, data)
	if err != nil {
		return nil, true, fmt.Errorf("binpkg: verify GPKG Manifest signature: %w", err)
	}
	return plain, true, nil
}

func extractClearSignedPayload(data []byte) ([]byte, error) {
	start := bytes.Index(data, []byte("\n\n"))
	end := bytes.Index(data, []byte("\n-----BEGIN PGP SIGNATURE-----"))
	if start < 0 || end < 0 || end <= start+2 {
		return nil, fmt.Errorf("binpkg: malformed clear-signed GPKG Manifest")
	}
	lines := bytes.Split(data[start+2:end], []byte("\n"))
	for index, line := range lines {
		if bytes.HasPrefix(line, []byte("- ")) {
			lines[index] = line[2:]
		}
	}
	return bytes.Join(lines, []byte("\n")), nil
}

func parseGPKGManifest(data []byte) (map[string]gpkgManifestRecord, error) {
	records := make(map[string]gpkgManifestRecord)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), maxGPKGManifestBytes)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 5 || fields[0] != "DATA" || strings.ContainsAny(fields[1], `/\`+"\x00") {
			return nil, fmt.Errorf("binpkg: invalid GPKG Manifest record")
		}
		if _, exists := records[fields[1]]; exists {
			return nil, fmt.Errorf("binpkg: duplicate GPKG Manifest record")
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 {
			return nil, fmt.Errorf("binpkg: invalid GPKG Manifest size")
		}
		record := gpkgManifestRecord{Name: fields[1], Size: size}
		seenDigests := make(map[string]struct{})
		if (len(fields)-3)%2 != 0 {
			return nil, fmt.Errorf("binpkg: invalid GPKG Manifest digest fields")
		}
		for index := 3; index < len(fields); index += 2 {
			if _, exists := seenDigests[fields[index]]; exists {
				return nil, fmt.Errorf("binpkg: duplicate GPKG Manifest digest")
			}
			seenDigests[fields[index]] = struct{}{}
			switch fields[index] {
			case "SHA512":
				if decoded, err := hex.DecodeString(fields[index+1]); err != nil || len(decoded) != sha512.Size {
					return nil, fmt.Errorf("binpkg: invalid GPKG SHA512 digest")
				}
				record.SHA512 = fields[index+1]
			case "BLAKE2B":
				if decoded, err := hex.DecodeString(fields[index+1]); err != nil || len(decoded) != blake2b.Size {
					return nil, fmt.Errorf("binpkg: invalid GPKG BLAKE2B digest")
				}
				record.BLAKE2B = fields[index+1]
			}
		}
		records[record.Name] = record
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("binpkg: read GPKG Manifest: %w", err)
	}
	return records, nil
}

func readGPKGMember(path, name string, limit int64) ([]byte, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	tr := tar.NewReader(file)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, "", fmt.Errorf("binpkg: GPKG member %q not found", name)
		}
		if err != nil {
			return nil, "", err
		}
		if hdr.Name != name {
			continue
		}
		if hdr.Size > limit {
			return nil, "", fmt.Errorf("binpkg: GPKG member %q exceeds limits", name)
		}
		data, err := io.ReadAll(io.LimitReader(tr, limit+1))
		if err != nil || int64(len(data)) != hdr.Size {
			return nil, "", fmt.Errorf("binpkg: read GPKG member %q: %w", name, err)
		}
		_, codec, _ := parseGPKGInnerName(filepath.Base(name))
		return data, codec, nil
	}
}

func parseGPKGInnerName(name string) (string, string, bool) {
	codecs := []struct{ suffix, codec string }{
		{".tar", ""}, {".tar.gz", "gzip"}, {".tar.bz2", "bzip2"},
		{".tar.xz", "xz"}, {".tar.zst", "zstd"}, {".tar.lz4", "lz4"},
		{".tar.lz", "lzip"}, {".tar.lzo", "lzop"},
	}
	for _, item := range codecs {
		if strings.HasSuffix(name, item.suffix) {
			return strings.TrimSuffix(name, item.suffix), item.codec, true
		}
	}
	return "", "", false
}

func decodeGPKGReader(ctx context.Context, source io.Reader, codec string) (io.Reader, func() error, error) {
	switch codec {
	case "":
		return source, func() error { return nil }, nil
	case "gzip":
		reader, err := gzip.NewReader(source)
		if err != nil {
			return nil, nil, err
		}
		return reader, reader.Close, nil
	case "bzip2":
		return bzip2.NewReader(source), func() error { return nil }, nil
	case "zstd":
		reader, err := zstd.NewReader(source, zstd.WithDecoderMaxMemory(uint64(DefaultExtractionPolicy.MaxTotalBytes)))
		if err != nil {
			return nil, nil, err
		}
		return reader, func() error { reader.Close(); return nil }, nil
	case "xz", "lz4", "lzip", "lzop":
		commands := map[string][]string{
			"xz": {"xz", "-d", "-c"}, "lz4": {"lz4", "-d", "-c"},
			"lzip": {"lzip", "-d", "-c"}, "lzop": {"lzop", "-d", "-c"},
		}
		arguments := commands[codec]
		command := exec.CommandContext(ctx, arguments[0], arguments[1:]...)
		command.Stdin = source
		output, err := command.StdoutPipe()
		if err != nil {
			return nil, nil, err
		}
		if err := command.Start(); err != nil {
			return nil, nil, err
		}
		return output, command.Wait, nil
	default:
		return nil, nil, fmt.Errorf("binpkg: unsupported GPKG compression %q", codec)
	}
}

func readGPKGMetadata(reader io.Reader) (map[string][]byte, error) {
	tr := tar.NewReader(io.LimitReader(reader, maxGPKGMetadataBytes+1))
	metadata := make(map[string][]byte)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("binpkg: read GPKG metadata archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return nil, fmt.Errorf("binpkg: GPKG metadata member %q is not a regular file", hdr.Name)
		}
		if !strings.HasPrefix(hdr.Name, "metadata/") {
			return nil, fmt.Errorf("binpkg: invalid GPKG metadata path %q", hdr.Name)
		}
		name := strings.TrimPrefix(hdr.Name, "metadata/")
		if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\`+"\x00") {
			return nil, fmt.Errorf("binpkg: unsafe GPKG metadata path %q", hdr.Name)
		}
		if _, exists := metadata[name]; exists {
			return nil, fmt.Errorf("binpkg: duplicate GPKG metadata %q", name)
		}
		if hdr.Size < 0 || total > maxGPKGMetadataBytes-hdr.Size {
			return nil, fmt.Errorf("binpkg: GPKG metadata exceeds limits")
		}
		data, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
		if err != nil || int64(len(data)) != hdr.Size {
			return nil, fmt.Errorf("binpkg: read GPKG metadata %q: %w", name, err)
		}
		total += hdr.Size
		metadata[name] = data
	}
	return metadata, nil
}

func untarPrefixed(reader io.Reader, destination string, policy ExtractionPolicy, prefix string) error {
	pipeReader, pipeWriter := io.Pipe()
	go func() {
		tw := tar.NewWriter(pipeWriter)
		tr := tar.NewReader(reader)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				err = tw.Close()
				_ = pipeWriter.CloseWithError(err)
				return
			}
			if err != nil {
				_ = pipeWriter.CloseWithError(err)
				return
			}
			expected := prefix + "/"
			if strings.TrimSuffix(hdr.Name, "/") == prefix {
				continue
			}
			if !strings.HasPrefix(hdr.Name, expected) {
				_ = pipeWriter.CloseWithError(fmt.Errorf("binpkg: GPKG image path %q lacks image prefix", hdr.Name))
				return
			}
			copyHeader := *hdr
			copyHeader.Name = strings.TrimPrefix(hdr.Name, expected)
			if copyHeader.Typeflag == tar.TypeLink {
				if !strings.HasPrefix(copyHeader.Linkname, expected) {
					_ = pipeWriter.CloseWithError(fmt.Errorf("binpkg: GPKG hardlink %q escapes image prefix", hdr.Name))
					return
				}
				copyHeader.Linkname = strings.TrimPrefix(copyHeader.Linkname, expected)
			}
			if err := tw.WriteHeader(&copyHeader); err != nil {
				_ = pipeWriter.CloseWithError(err)
				return
			}
			if _, err := io.Copy(tw, tr); err != nil {
				_ = pipeWriter.CloseWithError(err)
				return
			}
		}
	}()
	err := untar(pipeReader, destination, policy)
	_ = pipeReader.CloseWithError(err)
	return err
}

func CreateGPKG(ctx context.Context, request GPKGCreateRequest) error {
	if request.Path == "" || request.ImageRoot == "" || request.Basename == "" ||
		filepath.Base(request.Basename) != request.Basename || strings.ContainsAny(request.Basename, `/\`+"\x00") {
		return fmt.Errorf("binpkg: invalid GPKG creation request")
	}
	if request.ModTime.IsZero() {
		request.ModTime = time.Unix(0, 0).UTC()
	}
	image, err := buildGPKGImageArchive(ctx, request.ImageRoot, request.ModTime)
	if err != nil {
		return err
	}
	return createGPKGFromImageArchive(ctx, request, image)
}

func createGPKGFromImageArchive(ctx context.Context, request GPKGCreateRequest, image []byte) error {
	if request.Path == "" || request.Basename == "" || filepath.Base(request.Basename) != request.Basename ||
		strings.ContainsAny(request.Basename, `/\`+"\x00") {
		return fmt.Errorf("binpkg: invalid GPKG creation request")
	}
	if request.ModTime.IsZero() {
		request.ModTime = time.Unix(0, 0).UTC()
	}
	metadata, err := buildGPKGMetadataArchive(request.Metadata, request.ModTime)
	if err != nil {
		return err
	}
	metadata, err = compressGPKGZstd(metadata)
	if err != nil {
		return err
	}
	image, err = compressGPKGZstd(image)
	if err != nil {
		return err
	}
	type member struct {
		name string
		data []byte
	}
	members := []member{
		{GPKGVersion, nil},
		{"metadata.tar.zst", metadata},
		{"image.tar.zst", image},
	}
	var manifest bytes.Buffer
	for _, item := range members {
		shaSum := sha512.Sum512(item.data)
		blakeSum := blake2b.Sum512(item.data)
		fmt.Fprintf(&manifest, "DATA %s %d SHA512 %x BLAKE2B %x\n", item.name, len(item.data), shaSum, blakeSum)
	}
	manifestData := manifest.Bytes()
	if request.SignManifest != nil {
		manifestData, err = request.SignManifest(ctx, manifestData)
		if err != nil {
			return fmt.Errorf("binpkg: sign GPKG Manifest: %w", err)
		}
	}
	members = append(members, member{"Manifest", manifestData})
	if err := os.MkdirAll(filepath.Dir(request.Path), 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(request.Path), "."+filepath.Base(request.Path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	tw := tar.NewWriter(temporary)
	for _, item := range members {
		format := tar.FormatUSTAR
		if len(request.Basename+"/"+item.name) > 100 {
			format = tar.FormatGNU
		}
		hdr := &tar.Header{
			Name: request.Basename + "/" + item.name, Typeflag: tar.TypeReg,
			Mode: 0644, Size: int64(len(item.data)), ModTime: request.ModTime, Format: format,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(item.data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, request.Path); err != nil {
		return err
	}
	published = true
	return syncBinpkgDirectory(filepath.Dir(request.Path))
}

func compressGPKGZstd(data []byte) ([]byte, error) {
	var output bytes.Buffer
	writer, err := zstd.NewWriter(&output, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(data); err != nil {
		writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func syncBinpkgDirectory(path string) error {
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

func buildGPKGMetadataArchive(metadata map[string][]byte, modTime time.Time) ([]byte, error) {
	var result bytes.Buffer
	tw := tar.NewWriter(&result)
	names := make([]string, 0, len(metadata))
	for name := range metadata {
		if filepath.Base(name) != name || name == "" || strings.ContainsAny(name, `/\`+"\x00") {
			return nil, fmt.Errorf("binpkg: invalid GPKG metadata name %q", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data := metadata[name]
		if err := tw.WriteHeader(&tar.Header{
			Name: "metadata/" + name, Mode: 0644, Size: int64(len(data)),
			Typeflag: tar.TypeReg, ModTime: modTime, Format: tar.FormatUSTAR,
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

func buildGPKGImageArchive(ctx context.Context, root string, modTime time.Time) ([]byte, error) {
	var result bytes.Buffer
	tw := tar.NewWriter(&result)
	type inodeKey struct {
		dev uint64
		ino uint64
	}
	hardlinks := make(map[inodeKey]string)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := "image"
		if relative != "." {
			name += "/" + filepath.ToSlash(relative)
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name, hdr.ModTime, hdr.Format = name, modTime, tar.FormatPAX
		if info.Mode().IsRegular() {
			if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
				key := inodeKey{dev: uint64(stat.Dev), ino: stat.Ino}
				if first, exists := hardlinks[key]; exists {
					hdr.Typeflag, hdr.Linkname, hdr.Size = tar.TypeLink, first, 0
				} else {
					hardlinks[key] = name
				}
			}
		}
		if err := addFilesystemMetadata(hdr, path, info.Mode()&os.ModeSymlink != 0); err != nil {
			return err
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() && hdr.Typeflag != tar.TypeLink {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

func buildInstalledGPKGImageArchive(ctx context.Context, root string, entries []contentEntry) ([]byte, error) {
	var result bytes.Buffer
	tw := tar.NewWriter(&result)
	if err := tw.WriteHeader(&tar.Header{
		Name: "image", Typeflag: tar.TypeDir, Mode: 0755, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR,
	}); err != nil {
		return nil, err
	}
	type inodeKey struct {
		dev uint64
		ino uint64
	}
	hardlinks := make(map[inodeKey]string)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		archivePath, sourcePath, err := installedContentPath(root, entry.Path)
		if err != nil {
			return nil, err
		}
		if err := rejectSymlinkParents(root, sourcePath); err != nil {
			return nil, err
		}
		info, err := os.Lstat(sourcePath)
		if err != nil {
			return nil, err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(sourcePath)
			if err != nil {
				return nil, err
			}
			if entry.Type != "sym" || link != entry.Target {
				return nil, fmt.Errorf("binpkg: installed symlink %s changed", entry.Path)
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return nil, err
		}
		hdr.Name, hdr.Format = "image/"+archivePath, tar.FormatPAX
		if info.Mode().IsRegular() {
			if entry.Type != "obj" {
				return nil, fmt.Errorf("binpkg: installed path %s changed type", entry.Path)
			}
			if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
				key := inodeKey{dev: uint64(stat.Dev), ino: stat.Ino}
				if first, exists := hardlinks[key]; exists {
					hdr.Typeflag, hdr.Linkname, hdr.Size = tar.TypeLink, first, 0
				} else {
					hardlinks[key] = hdr.Name
				}
			}
		} else if info.IsDir() && entry.Type != "dir" {
			return nil, fmt.Errorf("binpkg: installed path %s changed type", entry.Path)
		} else if info.Mode()&os.ModeSymlink != 0 && entry.Type != "sym" {
			return nil, fmt.Errorf("binpkg: installed path %s changed type", entry.Path)
		}
		if err := addFilesystemMetadata(hdr, sourcePath, info.Mode()&os.ModeSymlink != 0); err != nil {
			return nil, err
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if info.Mode().IsRegular() && hdr.Typeflag != tar.TypeLink {
			file, err := os.Open(sourcePath)
			if err != nil {
				return nil, err
			}
			_, copyErr := io.Copy(tw, file)
			closeErr := file.Close()
			if copyErr != nil {
				return nil, copyErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}
