package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/recoveryset"
)

func writeUninstallNeeded(t *testing.T, vdb, cpv, metadata string) {
	t.Helper()
	dir := filepath.Join(vdb, filepath.FromSlash(cpv))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "NEEDED.ELF.2"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateELFRemovalOrder(t *testing.T) {
	vdb := t.TempDir()
	writeUninstallNeeded(t, vdb, "cat/provider-1", "X86_64;/usr/lib/libprovider.so.1;libprovider.so.1;;libc.so.6;x86_64\n")
	writeUninstallNeeded(t, vdb, "cat/consumer-1", "X86_64;/usr/bin/consumer;;;libprovider.so.1,libc.so.6;x86_64\n")

	if err := validateELFRemovalOrder(vdb, []string{"cat/consumer-1", "cat/provider-1"}); err != nil {
		t.Fatalf("safe consumer-first order rejected: %v", err)
	}
	if err := validateELFRemovalOrder(vdb, []string{"cat/provider-1", "cat/consumer-1"}); err == nil || !strings.Contains(err.Error(), "unsafe order") {
		t.Fatalf("provider-first order error=%v", err)
	}
	if err := validateELFRemovalOrder(vdb, []string{"cat/provider-1"}); err == nil || !strings.Contains(err.Error(), "outside the removal plan") {
		t.Fatalf("external consumer error=%v", err)
	}
	if err := validateELFRemovalOrder(vdb, []string{"cat/consumer-1", "cat/consumer-1"}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate error=%v", err)
	}
}

func TestPublishUninstallRecoverySetIncludesEveryRemovalBeforeReturning(t *testing.T) {
	paths := []string{"/vdb/cat/first-1", "/vdb/cat/second-2"}
	var captured recoveryset.Request
	wantErr := errors.New("injected publication failure")
	path, err := publishUninstallRecoverySet(context.Background(), paths, recoveryset.Request{SetID: "set"}, func(_ context.Context, request recoveryset.Request) (string, error) {
		captured = request
		return "", wantErr
	})
	if !errors.Is(err, wantErr) || path != "" {
		t.Fatalf("publishUninstallRecoverySet() = %q, %v", path, err)
	}
	if len(captured.Packages) != len(paths) {
		t.Fatalf("captured packages = %+v", captured.Packages)
	}
	for index, pkg := range captured.Packages {
		if pkg.VDBEntryPath != paths[index] {
			t.Fatalf("package %d = %q, want %q", index, pkg.VDBEntryPath, paths[index])
		}
	}
}

func TestPublishUninstallRecoverySetRejectsMissingPublisher(t *testing.T) {
	if _, err := publishUninstallRecoverySet(context.Background(), []string{"/vdb/cat/pkg-1"}, recoveryset.Request{}, nil); err == nil {
		t.Fatal("publishUninstallRecoverySet() accepted a missing publisher")
	}
}

func TestLifecycleNoopWithLiveRoot(t *testing.T) {
	clang := `pkg_postrm() {
	if [[ -z ${ROOT} && -f ${EPREFIX}/usr/share/eselect/modules/compiler-shadow.eselect ]] ; then
		eselect compiler-shadow clean all
	fi
}`
	if !lifecycleNoopWithLiveRoot(clang, "pkg_postrm") {
		t.Fatal("ROOT-empty guarded Clang hook rejected")
	}
	for name, body := range map[string]string{
		"unguarded": `pkg_postrm() {
	eselect compiler-shadow clean all
}`,
		"else": `pkg_postrm() {
	if [[ -z ${ROOT} ]] ; then
		true
	else
		eselect compiler-shadow clean all
	fi
}`,
		"different guard": `pkg_postrm() {
	if [[ -n ${ROOT} ]] ; then
		eselect compiler-shadow clean all
	fi
}`,
	} {
		t.Run(name, func(t *testing.T) {
			if lifecycleNoopWithLiveRoot(body, "pkg_postrm") {
				t.Fatal("unsafe lifecycle body accepted")
			}
		})
	}
}
