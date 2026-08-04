package env

import (
	crand "crypto/rand"
	"fmt"
	mrand "math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeEnvDir(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create env.d dir: %v", err)
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestReadEnvDirBasic(t *testing.T) {
	dir := t.TempDir()
	writeEnvDir(t, dir, map[string]string{
		"00basic":  `PATH="/usr/local/bin:/usr/bin"`,
		"90custom": `CUSTOM_VAR="hello world"`,
	})

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(entries), entries)
	}

	if entries[0].Name != "PATH" {
		t.Errorf("entry[0] name: got %q, want PATH", entries[0].Name)
	}
	if entries[1].Name != "CUSTOM_VAR" {
		t.Errorf("entry[1] name: got %q, want CUSTOM_VAR", entries[1].Name)
	}
	if entries[1].Value != "hello world" {
		t.Errorf("entry[1] value: got %q, want 'hello world'", entries[1].Value)
	}
}

func TestReadEnvDirEmpty(t *testing.T) {
	dir := t.TempDir()
	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir on empty dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestReadEnvDirMissing(t *testing.T) {
	entries, err := ReadEnvDir("/nonexistent/path/env.d")
	if err != nil {
		t.Fatalf("ReadEnvDir on missing dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for missing dir, got %d", len(entries))
	}
}

func TestReadEnvDirSkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	writeEnvDir(t, dir, map[string]string{
		"00basic": `PATH="/usr/bin"`,
	})
	subDir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry skipping subdir, got %d", len(entries))
	}
}

func TestReadEnvDirSkipsDotFiles(t *testing.T) {
	dir := t.TempDir()
	writeEnvDir(t, dir, map[string]string{
		"00basic": `PATH="/usr/bin"`,
		".hidden": `SECRET="nope"`,
		".swp":    `SECRET="nope"`,
	})

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry skipping dotfiles, got %d: %v", len(entries), entries)
	}
}

func TestReadEnvDirSortedOrder(t *testing.T) {
	dir := t.TempDir()
	writeEnvDir(t, dir, map[string]string{
		"99last":  `LAST="lastvalue"`,
		"00first": `FIRST="firstvalue"`,
		"50mid":   `MID="midvalue"`,
	})

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Name != "FIRST" {
		t.Errorf("entry[0] name: got %q, want FIRST", entries[0].Name)
	}
	if entries[1].Name != "MID" {
		t.Errorf("entry[1] name: got %q, want MID", entries[1].Name)
	}
	if entries[2].Name != "LAST" {
		t.Errorf("entry[2] name: got %q, want LAST", entries[2].Name)
	}
}

func TestParseEnvFileComments(t *testing.T) {
	dir := t.TempDir()
	writeEnvDir(t, dir, map[string]string{
		"test": `# This is a comment
VAR1=value1
  # indented comment
VAR2=value2`,
	})

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir: %v", err)
	}

	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	sort.Strings(names)

	if len(names) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(entries), names)
	}
}

func TestReadEnvDirVariableOverride(t *testing.T) {
	dir := t.TempDir()
	writeEnvDir(t, dir, map[string]string{
		"00basic":  `MYVAR="first definition"`,
		"99custom": `MYVAR="overridden value"`,
	})

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir: %v", err)
	}

	merged := mergeEnvEntries(entries)
	var found string
	for _, e := range merged {
		if e.Name == "MYVAR" {
			found = e.Value
		}
	}
	if found != "overridden value" {
		t.Errorf("MYVAR: got %q, want 'overridden value' (later file should win)", found)
	}
}

func TestPathDeduplication(t *testing.T) {
	dir := t.TempDir()
	writeEnvDir(t, dir, map[string]string{
		"00basic":  `PATH="/usr/local/bin:/usr/bin"`,
		"99custom": `PATH="/usr/local/bin:/opt/bin:/usr/bin"`,
		"50extra":  `PATH="/usr/local/sbin"`,
	})

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir: %v", err)
	}

	merged := mergeEnvEntries(entries)
	var pathVal string
	for _, e := range merged {
		if e.Name == "PATH" {
			pathVal = e.Value
		}
	}

	parts := strings.Split(pathVal, ":")
	seen := make(map[string]bool)

	expected := []string{"/usr/local/bin", "/usr/bin", "/opt/bin", "/usr/local/sbin"}
	expectedSet := make(map[string]bool)
	for _, p := range expected {
		expectedSet[p] = true
	}

	for _, p := range parts {
		if p == "" {
			continue
		}
		if seen[p] {
			t.Errorf("duplicate PATH entry: %s", p)
		}
		if !expectedSet[p] {
			t.Errorf("unexpected PATH entry: %s", p)
		}
		delete(expectedSet, p)
		seen[p] = true
	}

	for p := range expectedSet {
		t.Errorf("missing expected PATH entry: %s", p)
	}
}

func TestManpathDeduplication(t *testing.T) {
	dir := t.TempDir()
	writeEnvDir(t, dir, map[string]string{
		"00basic":  `MANPATH="/usr/share/man:/usr/local/share/man"`,
		"99custom": `MANPATH="/usr/share/man:/opt/share/man"`,
	})

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir: %v", err)
	}

	merged := mergeEnvEntries(entries)
	var val string
	for _, e := range merged {
		if e.Name == "MANPATH" {
			val = e.Value
		}
	}

	parts := strings.Split(val, ":")
	seen := make(map[string]bool)
	for _, p := range parts {
		if seen[p] {
			t.Errorf("duplicate MANPATH entry: %s", p)
		}
		seen[p] = true
	}

	if !seen["/usr/share/man"] {
		t.Error("missing /usr/share/man")
	}
	if !seen["/usr/local/share/man"] {
		t.Error("missing /usr/local/share/man")
	}
	if !seen["/opt/share/man"] {
		t.Error("missing /opt/share/man")
	}
}

func TestInfopathDeduplication(t *testing.T) {
	dir := t.TempDir()
	writeEnvDir(t, dir, map[string]string{
		"00basic": `INFOPATH="/usr/share/info:/usr/local/share/info"`,
		"50extra": `INFOPATH="/usr/share/info:/opt/share/info"`,
	})

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir: %v", err)
	}

	merged := mergeEnvEntries(entries)
	var val string
	for _, e := range merged {
		if e.Name == "INFOPATH" {
			val = e.Value
		}
	}

	parts := strings.Split(val, ":")
	seen := make(map[string]bool)
	for _, p := range parts {
		if seen[p] {
			t.Errorf("duplicate INFOPATH entry: %s", p)
		}
		seen[p] = true
	}
}

func TestLdpathDeduplication(t *testing.T) {
	dir := t.TempDir()
	writeEnvDir(t, dir, map[string]string{
		"00basic":  `LDPATH="/usr/lib64:/usr/local/lib64"`,
		"50custom": `LDPATH="/usr/lib64:/opt/lib64"`,
	})

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir: %v", err)
	}

	merged := mergeEnvEntries(entries)
	var val string
	for _, e := range merged {
		if e.Name == "LDPATH" {
			val = e.Value
		}
	}

	parts := strings.Split(val, ":")
	seen := make(map[string]bool)
	for _, p := range parts {
		if seen[p] {
			t.Errorf("duplicate LDPATH entry: %s", p)
		}
		seen[p] = true
	}
}

func TestPathOrderPreserved(t *testing.T) {
	dir := t.TempDir()
	writeEnvDir(t, dir, map[string]string{
		"00basic":  `PATH="/usr/bin"`,
		"99custom": `PATH="/opt/bin:/usr/local/bin"`,
	})

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir: %v", err)
	}

	merged := mergeEnvEntries(entries)
	var pathVal string
	for _, e := range merged {
		if e.Name == "PATH" {
			pathVal = e.Value
		}
	}

	parts := strings.Split(pathVal, ":")
	if parts[0] != "/usr/bin" {
		t.Errorf("first PATH entry should be /usr/bin, got %s", parts[0])
	}
}

func TestGenerateProfileEnv(t *testing.T) {
	entries := []EnvEntry{
		{Name: "PATH", Value: "/usr/local/bin:/usr/bin:/bin"},
		{Name: "MANPATH", Value: "/usr/share/man"},
		{Name: "MYVAR", Value: "hello world"},
		{Name: "LDPATH", Value: "/usr/lib64"},
		{Name: "ROOTPATH", Value: "/sbin:/usr/sbin"},
	}

	outDir := t.TempDir()
	profilePath := filepath.Join(outDir, "profile.env")

	if err := writeProfileEnv(profilePath, entries); err != nil {
		t.Fatalf("writeProfileEnv: %v", err)
	}

	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile.env: %v", err)
	}

	content := string(data)

	checks := []string{
		`export PATH=`,
		`export MANPATH=`,
		`export MYVAR="hello world"`,
		`export LDPATH=`,
		`export ROOTPATH=`,
	}

	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("profile.env missing: %s", check)
		}
	}

	if !strings.Contains(content, "/usr/local/bin:/usr/bin:/bin") {
		t.Errorf("profile.env missing PATH value")
	}
}

func TestGenerateCshEnv(t *testing.T) {
	entries := []EnvEntry{
		{Name: "PATH", Value: "/usr/local/bin:/usr/bin:/bin"},
		{Name: "MYVAR", Value: "hello"},
	}

	outDir := t.TempDir()
	cshPath := filepath.Join(outDir, "csh.env")

	if err := writeCshEnv(cshPath, entries); err != nil {
		t.Fatalf("writeCshEnv: %v", err)
	}

	data, err := os.ReadFile(cshPath)
	if err != nil {
		t.Fatalf("read csh.env: %v", err)
	}

	content := string(data)

	if !strings.Contains(content, "setenv PATH") {
		t.Error("csh.env missing setenv PATH")
	}
	if !strings.Contains(content, "setenv MYVAR") {
		t.Error("csh.env missing setenv MYVAR")
	}
}

func TestUpdateEnvFull(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "env.d")
	outDir := t.TempDir()

	writeEnvDir(t, envDir, map[string]string{
		"00basic":  `PATH="/usr/local/bin:/usr/bin:/bin"`,
		"10man":    `MANPATH="/usr/share/man"`,
		"99custom": `MYVAR="custom_value"`,
	})

	if err := UpdateEnv(envDir, outDir); err != nil {
		t.Fatalf("UpdateEnv: %v", err)
	}

	profileData, err := os.ReadFile(filepath.Join(outDir, "profile.env"))
	if err != nil {
		t.Fatalf("read profile.env: %v", err)
	}

	cshData, err := os.ReadFile(filepath.Join(outDir, "csh.env"))
	if err != nil {
		t.Fatalf("read csh.env: %v", err)
	}

	profile := string(profileData)
	csh := string(cshData)

	if !strings.Contains(profile, `MYVAR=custom_value`) && !strings.Contains(profile, `MYVAR="custom_value"`) {
		t.Error("profile.env missing MYVAR")
	}
	if !strings.Contains(csh, `MYVAR`) {
		t.Error("csh.env missing MYVAR")
	}
}

func TestUpdateEnvEmptyEnvDir(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "env.d")
	outDir := t.TempDir()

	if err := UpdateEnv(envDir, outDir); err != nil {
		t.Fatalf("UpdateEnv with empty env.d: %v", err)
	}
}

func TestUpdateEnvDefaultPaths(t *testing.T) {
	envDir, outputDir := updateEnvPaths("", "")
	if envDir != "/etc/env.d" || outputDir != "/etc" {
		t.Fatalf("default paths = %q, %q", envDir, outputDir)
	}
	envDir, outputDir = updateEnvPaths("/custom/env.d", "/custom/etc")
	if envDir != "/custom/env.d" || outputDir != "/custom/etc" {
		t.Fatalf("explicit paths = %q, %q", envDir, outputDir)
	}
}

func TestBadlyFormattedLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	writeEnvDir(t, dir, map[string]string{
		"00basic": `#comment
# another comment

PATH="/usr/bin"

# this line has no equals sign
MISSING_EQUALS
   
VAR_WITH_SPACES  =  "hello"`,
	})

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir: %v", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}

	if len(names) != 2 {
		t.Errorf("expected 2 valid entries (PATH and VAR_WITH_SPACES), got %d: %v", len(names), names)
	}

	if entries[0].Name != "PATH" {
		t.Errorf("expected PATH entry, got %s", entries[0].Name)
	}
}

func TestRunLdConfig(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "sbin", "ldconfig")
	if err := os.MkdirAll(filepath.Dir(tool), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RunLdConfig(root); err != nil {
		t.Fatalf("RunLdConfig: %v", err)
	}
}

func TestAdversarialMassiveEnvDir(t *testing.T) {
	dir := t.TempDir()

	files := map[string]string{}
	for i := 0; i < 500; i++ {
		name := fmt.Sprintf("%02d-file", i)
		var buf strings.Builder
		buf.WriteString(fmt.Sprintf("VAR_%d=value_%d\n", i, i))
		if i%3 == 0 {
			buf.WriteString(fmt.Sprintf("PATH=/path/%d\n", i))
		}
		files[name] = buf.String()
	}

	writeEnvDir(t, dir, files)

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir massive: %v", err)
	}

	if len(entries) < 500 {
		t.Errorf("expected at least 500 entries, got %d", len(entries))
	}
}

func TestAdversarialBinaryContent(t *testing.T) {
	dir := t.TempDir()

	garbage := make([]byte, 4096)
	_, _ = crand.Read(garbage)
	for i := range garbage {
		if garbage[i] == 0 {
			garbage[i] = 0x41
		}
	}
	garbage[len(garbage)/2] = byte('=')

	if err := os.WriteFile(filepath.Join(dir, "binary"), garbage, 0644); err != nil {
		t.Fatalf("write binary file: %v", err)
	}

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir with binary: %v", err)
	}

	_ = entries
}

func TestAdversarialLongLine(t *testing.T) {
	dir := t.TempDir()

	value := strings.Repeat("x", 100000)
	content := "LONGVAR=" + value

	writeEnvDir(t, dir, map[string]string{
		"long": content,
	})

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir long line: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if len(entries[0].Value) != 100000 {
		t.Errorf("expected 100000 char value, got %d", len(entries[0].Value))
	}
}

func TestMutationRandomEntry(t *testing.T) {
	dir := t.TempDir()
	valid := `PATH="/usr/bin"
MYVAR="hello"
ANOTHER=value
`

	rng := mrand.New(mrand.NewSource(42))
	for i := 0; i < 50; i++ {
		mutated := []byte(valid)
		pos := rng.Intn(len(mutated))
		mutated[pos] = byte(rng.Intn(256))
		if mutated[pos] == 0 {
			mutated[pos] = 0x41
		}

		path := filepath.Join(dir, fmt.Sprintf("mutated_%d", i))
		if err := os.WriteFile(path, mutated, 0644); err != nil {
			t.Fatalf("write mutated: %v", err)
		}
	}

	entries, err := ReadEnvDir(dir)
	if err != nil {
		t.Fatalf("ReadEnvDir mutated: %v", err)
	}
	_ = entries
}

func TestMergeDedupOrdering(t *testing.T) {
	entries := []EnvEntry{
		{Name: "PATH", Value: "/usr/bin"},
		{Name: "MANPATH", Value: "/usr/share/man"},
		{Name: "KVAR", Value: "first"},
		{Name: "PATH", Value: "/opt/bin:/usr/bin"},
		{Name: "KVAR", Value: "second"},
		{Name: "MANPATH", Value: "/usr/share/man:/usr/local/share/man"},
	}

	merged := mergeEnvEntries(entries)

	var pathEntry *EnvEntry
	var manEntry *EnvEntry
	var kvarEntry *EnvEntry
	for i := range merged {
		switch merged[i].Name {
		case "PATH":
			pathEntry = &merged[i]
		case "MANPATH":
			manEntry = &merged[i]
		case "KVAR":
			kvarEntry = &merged[i]
		}
	}

	if kvarEntry == nil || kvarEntry.Value != "second" {
		t.Error("KVAR should retain the last value (second)")
	}

	if pathEntry == nil {
		t.Fatal("PATH entry missing")
	}
	pathParts := strings.Split(pathEntry.Value, ":")
	if len(pathParts) != 2 || pathParts[0] != "/usr/bin" || pathParts[1] != "/opt/bin" {
		t.Errorf("PATH: got %q, want /usr/bin:/opt/bin", pathEntry.Value)
	}

	if manEntry == nil {
		t.Fatal("MANPATH entry missing")
	}
	manParts := strings.Split(manEntry.Value, ":")
	if len(manParts) != 2 {
		t.Errorf("MANPATH: got %q, want 2 deduplicated parts", manEntry.Value)
	}
}

func TestQuoteBashNoSpecialChars(t *testing.T) {
	result := quoteBash("simple")
	if result != "simple" {
		t.Errorf("quoteBash simple: got %q, want %q", result, "simple")
	}
}

func TestQuoteBashWithSpaces(t *testing.T) {
	result := quoteBash("hello world")
	if result != `"hello world"` {
		t.Errorf("quoteBash with spaces: got %q", result)
	}
}

func TestQuoteBashWithQuotes(t *testing.T) {
	result := quoteBash(`say "hello"`)
	if !strings.Contains(result, `\"`) {
		t.Errorf("quoteBash with quotes: got %q", result)
	}
}

func TestUnquoteShDouble(t *testing.T) {
	result := unquoteSh(`"hello"`)
	if result != "hello" {
		t.Errorf("unquoteSh double: got %q, want hello", result)
	}
}

func TestUnquoteShSingle(t *testing.T) {
	result := unquoteSh(`'hello'`)
	if result != "hello" {
		t.Errorf("unquoteSh single: got %q, want hello", result)
	}
}

func TestUnquoteShNoQuotes(t *testing.T) {
	result := unquoteSh("hello")
	if result != "hello" {
		t.Errorf("unquoteSh no quotes: got %q, want hello", result)
	}
}
