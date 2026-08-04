package binpkg

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGPKGCreateReadExtractRoundTrip(t *testing.T) {
	base := t.TempDir()
	image := filepath.Join(base, "image")
	if err := os.MkdirAll(filepath.Join(image, "usr", "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(image, "usr", "bin", "fixture"), []byte("payload"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "fixture-1.gpkg.tar")
	metadata := map[string][]byte{
		"CATEGORY": []byte("app-test\n"), "PF": []byte("fixture-1\n"),
		"SLOT": []byte("0/1\n"), "EAPI": []byte("8\n"), "USE": []byte("feature\n"),
	}
	if err := CreateGPKG(context.Background(), GPKGCreateRequest{
		Path: path, Basename: "fixture-1", ImageRoot: image, Metadata: metadata,
		ModTime: time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatal(err)
	}
	pkg, err := ReadGPKG(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Prefix != "fixture-1" || pkg.Signed || string(pkg.Metadata["CATEGORY"]) != "app-test\n" {
		t.Fatalf("GPKG = %+v", pkg)
	}
	info, err := ReadInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.CPV() != "app-test/fixture-1" || info.Slot != "0" || info.Subslot != "1" {
		t.Fatalf("ReadInfo() = %+v", info)
	}
	destination := filepath.Join(base, "destination")
	if err := Extract(context.Background(), path, destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "usr", "bin", "fixture"))
	if err != nil || string(data) != "payload" {
		t.Fatalf("extracted payload = %q, %v", data, err)
	}
}

func TestCreateInstalledGPKGCapturesOnlyContentsAndFullVDBMetadata(t *testing.T) {
	base := t.TempDir()
	vdb, root := createCaptureFixture(t, base, "obj /usr/bin/fixture 7 1700000000\n")
	if err := os.WriteFile(filepath.Join(root, "usr", "bin", "fixture"), []byte("payload"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr", "bin", "foreign"), []byte("exclude"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vdb, "environment.bz2"), []byte("environment"), 0644); err != nil {
		t.Fatal(err)
	}
	path, err := CreateInstalledGPKG(context.Background(), vdb, root, filepath.Join(base, "packages"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "sys-devel/fixture/fixture-1.0.gpkg.tar") {
		t.Fatalf("GPKG path = %s", path)
	}
	pkg, err := ReadGPKG(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if string(pkg.Metadata["environment.bz2"]) != "environment" {
		t.Fatalf("saved environment = %q", pkg.Metadata["environment.bz2"])
	}
	destination := filepath.Join(base, "destination")
	if err := Extract(context.Background(), path, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "usr", "bin", "fixture")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "usr", "bin", "foreign")); !os.IsNotExist(err) {
		t.Fatalf("foreign file was captured: %v", err)
	}
}

func TestGPKGManifestTamperingAndSignaturePolicyFailClosed(t *testing.T) {
	base := t.TempDir()
	image := filepath.Join(base, "image")
	if err := os.MkdirAll(image, 0755); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(image, "payload")
	if err := os.WriteFile(payload, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	unsigned := filepath.Join(base, "unsigned.gpkg.tar")
	if err := CreateGPKG(context.Background(), GPKGCreateRequest{
		Path: unsigned, Basename: "fixture-1", ImageRoot: image,
		Metadata: map[string][]byte{"CATEGORY": []byte("app-test")},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadGPKGWithPolicy(context.Background(), unsigned, GPKGPolicy{
		RequireSignature: true, Extraction: DefaultExtractionPolicy,
	}); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("required-signature error = %v", err)
	}
	signed := filepath.Join(base, "signed.gpkg.tar")
	sign := func(_ context.Context, manifest []byte) ([]byte, error) {
		return []byte("-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA512\n\n" +
			string(manifest) + "-----BEGIN PGP SIGNATURE-----\nfake\n-----END PGP SIGNATURE-----\n"), nil
	}
	if err := CreateGPKG(context.Background(), GPKGCreateRequest{
		Path: signed, Basename: "fixture-1", ImageRoot: image,
		Metadata: map[string][]byte{"CATEGORY": []byte("app-test")}, SignManifest: sign,
	}); err != nil {
		t.Fatal(err)
	}
	verifierCalled := false
	verifier := func(_ context.Context, signed []byte) ([]byte, error) {
		verifierCalled = true
		return extractClearSignedPayload(signed)
	}
	if _, err := ReadGPKGWithPolicy(context.Background(), signed, GPKGPolicy{
		RequireSignature: true, VerifyManifest: verifier, Extraction: DefaultExtractionPolicy,
	}); err != nil {
		t.Fatal(err)
	}
	if !verifierCalled {
		t.Fatal("signature verifier was not called")
	}
	reject := func(context.Context, []byte) ([]byte, error) { return nil, errors.New("untrusted key") }
	if _, err := ReadGPKGWithPolicy(context.Background(), signed, GPKGPolicy{
		RequireSignature: true, VerifyManifest: reject, Extraction: DefaultExtractionPolicy,
	}); err == nil || !strings.Contains(err.Error(), "untrusted") {
		t.Fatalf("untrusted signature error = %v", err)
	}
}

func TestCreateGPKGIsDeterministicAndSigningFailureIsAtomic(t *testing.T) {
	base := t.TempDir()
	image := filepath.Join(base, "image")
	if err := os.MkdirAll(image, 0755); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(image, "payload")
	if err := os.WriteFile(payload, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Unix(1_700_000_000, 0)
	first, second := filepath.Join(base, "first.gpkg.tar"), filepath.Join(base, "second.gpkg.tar")
	for index, path := range []string{first, second} {
		if err := CreateGPKG(context.Background(), GPKGCreateRequest{
			Path: path, Basename: "fixture-1", ImageRoot: image,
			Metadata: map[string][]byte{"PF": []byte("fixture-1\n")}, ModTime: modTime,
		}); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			info, err := os.Stat(payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(payload, modTime.Add(time.Hour), info.ModTime()); err != nil {
				t.Fatal(err)
			}
		}
	}
	firstData, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstData, secondData) {
		t.Fatal("identical GPKG inputs produced different bytes")
	}
	failed := filepath.Join(base, "failed.gpkg.tar")
	err = CreateGPKG(context.Background(), GPKGCreateRequest{
		Path: failed, Basename: "fixture-1", ImageRoot: image,
		Metadata: map[string][]byte{"PF": []byte("fixture-1\n")},
		SignManifest: func(context.Context, []byte) ([]byte, error) {
			return nil, errors.New("injected signing failure")
		},
	})
	if err == nil {
		t.Fatal("CreateGPKG() accepted signing failure")
	}
	if _, statErr := os.Stat(failed); !os.IsNotExist(statErr) {
		t.Fatalf("failed GPKG became visible: %v", statErr)
	}
	temporaries, globErr := filepath.Glob(filepath.Join(base, ".failed.gpkg.tar.tmp-*"))
	if globErr != nil || len(temporaries) != 0 {
		t.Fatalf("failed GPKG retained temporaries %v: %v", temporaries, globErr)
	}
}

func TestInstalledGPKGImageIgnoresAccessTime(t *testing.T) {
	root := t.TempDir()
	payload := filepath.Join(root, "payload")
	if err := os.WriteFile(payload, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := buildInstalledGPKGImageArchive(context.Background(), root, []contentEntry{{Type: "obj", Path: "/payload"}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(payload, time.Unix(1_800_000_000, 0), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, err := buildInstalledGPKGImageArchive(context.Background(), root, []contentEntry{{Type: "obj", Path: "/payload"}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("installed GPKG image changed with access time")
	}
}

func TestGPKGRejectsOuterTraversalDuplicateAndManifestMismatch(t *testing.T) {
	tests := []struct {
		name    string
		headers []*tar.Header
		bodies  [][]byte
	}{
		{
			name:    "traversal",
			headers: []*tar.Header{{Name: "../gpkg-1", Mode: 0644, Size: 0}},
			bodies:  [][]byte{nil},
		},
		{
			name: "duplicate",
			headers: []*tar.Header{
				{Name: "pkg/gpkg-1", Mode: 0644, Size: 0},
				{Name: "pkg/gpkg-1", Mode: 0644, Size: 0},
			},
			bodies: [][]byte{nil, nil},
		},
		{
			name: "manifest mismatch",
			headers: []*tar.Header{
				{Name: "pkg/gpkg-1", Mode: 0644, Size: 0},
				{Name: "pkg/Manifest", Mode: 0644, Size: int64(len("DATA gpkg-1 1 SHA512 bad\n"))},
			},
			bodies: [][]byte{nil, []byte("DATA gpkg-1 1 SHA512 bad\n")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.gpkg.tar")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			writer := tar.NewWriter(file)
			for index, header := range test.headers {
				if err := writer.WriteHeader(header); err != nil {
					t.Fatal(err)
				}
				if _, err := writer.Write(test.bodies[index]); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadGPKG(context.Background(), path); err == nil {
				t.Fatalf("ReadGPKG() accepted %s", test.name)
			}
		})
	}
}

func TestAriseGPKGIsReadableByInstalledPortage(t *testing.T) {
	python, err := exec.LookPath("python3.14")
	if err != nil {
		t.Skip("installed Portage Python is unavailable")
	}
	base := t.TempDir()
	image := filepath.Join(base, "image")
	if err := os.MkdirAll(image, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "fixture-1.gpkg.tar")
	if err := CreateGPKG(context.Background(), GPKGCreateRequest{
		Path: path, Basename: "fixture-1", ImageRoot: image,
		Metadata: map[string][]byte{"CATEGORY": []byte("app-test\n"), "PF": []byte("fixture-1\n")},
	}); err != nil {
		t.Fatal(err)
	}
	script := "import portage,sys\nfrom portage.gpkg import gpkg\np=gpkg(portage.settings,basename='fixture-1',gpkg_file=sys.argv[1],verify_signature=False)\nsys.stdout.buffer.write(p.get_metadata('CATEGORY'))\n"
	command := exec.Command(python, "-c", script, path)
	command.Env = append(os.Environ(), "PORTAGE_CONFIGROOT="+isolatedPortageConfigRoot(t))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Portage rejected Arise GPKG: %v\n%s", err, output)
	}
	if !bytes.Equal(output, []byte("app-test\n")) {
		t.Fatalf("Portage metadata = %q", output)
	}
}

func TestPortageGPKGIsReadableByArise(t *testing.T) {
	python, err := exec.LookPath("python3.14")
	if err != nil {
		t.Skip("installed Portage Python is unavailable")
	}
	base := t.TempDir()
	image := filepath.Join(base, "image")
	if err := os.MkdirAll(image, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(image, "payload"), []byte("portage"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "fixture-1.gpkg.tar")
	script := "import portage,sys\nfrom portage.gpkg import gpkg\ns=portage.config(clone=portage.settings)\ns['BINPKG_COMPRESS']='zstd'\np=gpkg(s,basename='fixture-1',gpkg_file=sys.argv[1],verify_signature=False)\np.compress(sys.argv[2],{'CATEGORY':b'app-test\\n','PF':b'fixture-1\\n','SLOT':b'0\\n','EAPI':b'8\\n'})\n"
	command := exec.Command(python, "-c", script, path, image)
	command.Env = append(os.Environ(), "PORTAGE_CONFIGROOT="+isolatedPortageConfigRoot(t))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Portage fixture creation failed: %v\n%s", err, output)
	}
	pkg, err := ReadGPKG(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if string(pkg.Metadata["CATEGORY"]) != "app-test\n" || pkg.ImageCodec != "zstd" {
		t.Fatalf("Portage GPKG = %+v", pkg)
	}
	destination := filepath.Join(base, "destination")
	if err := Extract(context.Background(), path, destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "payload"))
	if err != nil || string(data) != "portage" {
		t.Fatalf("Portage payload = %q, %v", data, err)
	}
}

func isolatedPortageConfigRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc", "portage"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}
