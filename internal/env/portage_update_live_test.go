//go:build live_portage

package env

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestUpdateRootMatchesInstalledPortage(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3/Portage reference unavailable")
	}
	ariseRoot, portageRoot := filepath.Join(t.TempDir(), "arise"), filepath.Join(t.TempDir(), "portage")
	files := map[string]string{
		"00basic": "PATH=/bin:/usr/bin\nLDPATH=/lib:/usr/lib\nCONFIG_PROTECT=/etc\n",
		"50more":  "PATH=/usr/bin:/opt/bin\nLDPATH=/usr/lib:/opt/lib\nCUSTOM=value\n",
		"70types": "COLON_SEPARATED=\"EXTRA_PATH\"\nSPACE_SEPARATED=\"EXTRA_WORDS\"\nEXTRA_PATH=\"/one:/two\"\nEXTRA_WORDS=\"one two\"\n",
	}
	for _, root := range []string{ariseRoot, portageRoot} {
		directory := filepath.Join(root, "etc", "env.d")
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := UpdateRoot(ariseRoot, "", false); err != nil {
		t.Fatal(err)
	}
	program := "from portage.util.env_update import env_update; env_update(makelinks=0, target_root=" + pythonString(portageRoot) + ")"
	if output, err := exec.Command(python, "-c", program).CombinedOutput(); err != nil {
		t.Fatalf("Portage env_update: %v: %s", err, output)
	}
	for _, relative := range []string{"etc/ld.so.conf", "etc/profile.env", "etc/csh.env", "etc/environment.d/10-gentoo-env.conf"} {
		got, gotErr := os.ReadFile(filepath.Join(ariseRoot, relative))
		want, wantErr := os.ReadFile(filepath.Join(portageRoot, relative))
		if gotErr != nil || wantErr != nil {
			t.Fatalf("read %s: Arise=%v Portage=%v", relative, gotErr, wantErr)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs\nArise:\n%s\nPortage:\n%s", relative, got, want)
		}
	}
}

func TestUpdateRootBuildsDisposableLinuxLinkerCache(t *testing.T) {
	ldconfig, err := exec.LookPath("ldconfig")
	if err != nil {
		t.Skip("ldconfig unavailable")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc", "env.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "env.d", "00basic"), []byte("LDPATH=/lib:/usr/lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := UpdateRoot(root, ldconfig, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.LdconfigRan {
		t.Fatal("ldconfig was not recorded")
	}
	if info, err := os.Stat(filepath.Join(root, "etc", "ld.so.cache")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("disposable linker cache: info=%v err=%v", info, err)
	}
}

func pythonString(value string) string {
	quoted := "'"
	for _, char := range value {
		if char == '\'' {
			quoted += "\\'"
		} else {
			quoted += string(char)
		}
	}
	return quoted + "'"
}
