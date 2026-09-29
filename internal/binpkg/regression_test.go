package binpkg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAuditGPKGExtractionUsesVerifiedFile(t *testing.T) {
	for _, mutation := range []string{"replace", "overwrite"} {
		for _, operation := range []string{"metadata", "extract"} {
			t.Run(mutation+"/"+operation, func(t *testing.T) {
				ctx := context.Background()
				base := t.TempDir()
				image := filepath.Join(base, "image")
				if err := os.MkdirAll(image, 0755); err != nil {
					t.Fatal(err)
				}
				payload := filepath.Join(image, "payload")
				if err := os.WriteFile(payload, []byte("trusted"), 0644); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(base, "trusted.gpkg.tar")
				sign := func(_ context.Context, manifest []byte) ([]byte, error) {
					return []byte("-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA512\n\n" + string(manifest) + "-----BEGIN PGP SIGNATURE-----\nfixture\n-----END PGP SIGNATURE-----\n"), nil
				}
				if err := CreateGPKG(ctx, GPKGCreateRequest{Path: path, Basename: "pkg-1", ImageRoot: image, Metadata: map[string][]byte{"CATEGORY": []byte("trusted")}, SignManifest: sign}); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(payload, []byte("unverified replacement"), 0644); err != nil {
					t.Fatal(err)
				}
				replacement := filepath.Join(base, "replacement.gpkg.tar")
				if err := CreateGPKG(ctx, GPKGCreateRequest{Path: replacement, Basename: "pkg-1", ImageRoot: image, Metadata: map[string][]byte{"CATEGORY": []byte("forged")}}); err != nil {
					t.Fatal(err)
				}
				policy := GPKGPolicy{RequireSignature: true, Extraction: DefaultExtractionPolicy, VerifyManifest: func(_ context.Context, signed []byte) ([]byte, error) {
					plain, err := extractClearSignedPayload(signed)
					if err != nil {
						return nil, err
					}
					if mutation == "replace" {
						err = os.Rename(replacement, path)
					} else {
						var data []byte
						data, err = os.ReadFile(replacement)
						if err == nil {
							err = os.WriteFile(path, data, 0644)
						}
					}
					return plain, err
				}}
				if operation == "metadata" {
					pkg, err := ReadGPKGWithPolicy(ctx, path, policy)
					if err != nil {
						t.Fatal(err)
					}
					if string(pkg.Metadata["CATEGORY"]) != "trusted" {
						t.Fatalf("unverified metadata: %q", pkg.Metadata["CATEGORY"])
					}
					return
				}
				destination := filepath.Join(base, "destination")
				if err := ExtractGPKG(ctx, path, destination, policy); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(filepath.Join(destination, "payload"))
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != "trusted" {
					t.Fatalf("unverified payload: %q", got)
				}
			})
		}
	}
}
