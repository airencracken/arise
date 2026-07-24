package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateRootGeneratesPortageEnvironmentAndLinkerInputs(t *testing.T) {
	root := t.TempDir()
	envDir := filepath.Join(root, "etc", "env.d")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"00basic": "PATH=/bin:/usr/bin\nLDPATH=/lib:/usr/lib\nCONFIG_PROTECT=/etc\n",
		"50more":  "PATH=/usr/bin:/opt/bin\nLDPATH=/usr/lib:/opt/lib\nCUSTOM=last\n",
		"ignored": "CUSTOM=wrong\n",
	} {
		if err := os.WriteFile(filepath.Join(envDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := UpdateRoot(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.EnvironmentFiles != 2 || strings.Join(result.LinkerPaths, ":") != "/lib:/usr/lib:/opt/lib" {
		t.Fatalf("result=%+v", result)
	}
	assertContains := func(name string, values ...string) {
		t.Helper()
		content, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, value := range values {
			if !strings.Contains(string(content), value) {
				t.Errorf("%s missing %q:\n%s", name, value, content)
			}
		}
	}
	assertContains("etc/ld.so.conf", "/lib\n/usr/lib\n/opt/lib\n")
	assertContains("etc/profile.env", "export PATH='/bin:/usr/bin:/opt/bin'", "export CUSTOM='last'")
	assertContains("etc/csh.env", "setenv PATH '/bin:/usr/bin:/opt/bin'")
	assertContains("etc/environment.d/10-gentoo-env.conf", "PATH=/bin:/usr/bin:/opt/bin")
}

func TestUpdateRootRunsLdconfigWithPreserveLibsSafeArguments(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc", "env.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	arguments := filepath.Join(root, "arguments")
	tool := filepath.Join(root, "ldconfig-test")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellSingleQuote(arguments) + "\n"
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := UpdateRoot(root, tool, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.LdconfigRan {
		t.Fatal("ldconfig did not run")
	}
	data, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "-X\n-r\n"+root+"\n"; got != want {
		t.Fatalf("arguments=%q want %q", got, want)
	}
}
