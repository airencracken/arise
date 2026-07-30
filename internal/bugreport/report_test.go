package bugreport

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestCollectSchemaIsDeterministicAndDefaultDeny(t *testing.T) {
	redactor := NewRedactor("/home/alice", "workstation")
	options := Options{
		Version: "0.1", Invocation: []string{"arise", "install", "cat/pkg"},
		Package: "cat/pkg", PlanSHA256: strings.Repeat("a", 64), Redactor: redactor,
	}
	first, second := Collect(options), Collect(options)
	firstJSON, err := JSON(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := JSON(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("non-deterministic JSON:\n%s\n%s", firstJSON, secondJSON)
	}
	var document map[string]any
	if err := json.Unmarshal(firstJSON, &document); err != nil {
		t.Fatal(err)
	}
	if document["schema"] != float64(SchemaVersion) || document["complete"] != true {
		t.Fatalf("schema contract=%v", document)
	}
	if _, exists := document["environment"]; exists {
		t.Fatal("collector admitted arbitrary environment")
	}
}

func TestCollectRedactsHostileInputsAcrossEveryArtifact(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "build.log")
	hostile := "password=hunter2 token:abc https://alice:pw@private.example/repo?q=secret /home/alice workstation\n```\n# injected"
	if err := os.WriteFile(logPath, []byte(hostile), 0o600); err != nil {
		t.Fatal(err)
	}
	report := Collect(Options{
		Version: hostile, Invocation: []string{"arise", "--proxy=" + hostile}, Package: hostile,
		LogPaths: []string{logPath}, Redactor: NewRedactor("/home/alice", "workstation"),
	})
	jsonData, err := JSON(report)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := string(jsonData) + Markdown(report)
	for _, secret := range []string{"hunter2", "abc", "alice:pw", "q=secret", "/home/alice", "workstation"} {
		if strings.Contains(artifacts, secret) {
			t.Fatalf("secret %q survived:\n%s", secret, artifacts)
		}
	}
	if strings.Contains(Markdown(report), "\n```\n# injected") {
		t.Fatal("log escaped its Markdown indentation")
	}
}

func TestCollectCorruptAndOversizedStateFailsPartiallyWithoutPanicking(t *testing.T) {
	root := t.TempDir()
	resume := filepath.Join(root, "resume")
	logPath := filepath.Join(root, "huge.log")
	if err := os.WriteFile(resume, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, bytes.Repeat([]byte("x"), 33), 0o600); err != nil {
		t.Fatal(err)
	}
	report := Collect(Options{ResumePath: resume, JournalDir: filepath.Join(root, "missing"), LogPaths: []string{logPath}, MaxLogBytes: 32})
	if report.Complete || len(report.Warnings) != 2 || len(report.Logs) != 0 {
		t.Fatalf("partial report=%#v", report)
	}
}

func TestInvalidPlanDigestIsExcluded(t *testing.T) {
	report := Collect(Options{PlanSHA256: "../../not-a-digest"})
	if report.Complete || report.PlanSHA256 != "" || len(report.Warnings) != 1 {
		t.Fatalf("invalid digest report=%#v", report)
	}
}

func TestArchiveIsDeterministicAndContainsOnlyContractFiles(t *testing.T) {
	report := Collect(Options{Version: "test", Invocation: []string{"arise", "bug-report"}})
	var first, second bytes.Buffer
	if err := WriteArchive(&first, report); err != nil {
		t.Fatal(err)
	}
	if err := WriteArchive(&second, report); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("archive bytes are not deterministic")
	}
	decoder, err := zstd.NewReader(bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	tr := tar.NewReader(decoder)
	var names []string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
		if header.Mode != 0o600 || header.ModTime.Unix() != 0 {
			t.Fatalf("unsafe/non-deterministic header=%#v", header)
		}
	}
	if want := []string{"report.json", "report.md"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("archive files=%v, want %v", names, want)
	}
}

func TestWriteDirectoryRefusesExistingOutputAndUsesPrivateModes(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "report")
	report := Collect(Options{Version: "test"})
	if err := WriteDirectory(output, report); err != nil {
		t.Fatal(err)
	}
	if err := WriteDirectory(output, report); err == nil {
		t.Fatal("existing output was overwritten")
	}
	for _, path := range []string{output, filepath.Join(output, "report.json"), filepath.Join(output, "report.md")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode=%#o, want %#o", path, info.Mode().Perm(), want)
		}
	}
}

func TestWriteDirectoryFailureBeforePublishIsAtomic(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "report")
	original := writeReportFile
	writeReportFile = func(path string, data []byte) error {
		if strings.HasSuffix(path, "report.md") {
			return errors.New("injected write failure")
		}
		return writeSynced(path, data)
	}
	defer func() { writeReportFile = original }()
	if err := WriteDirectory(output, Collect(Options{Version: "test"})); err == nil {
		t.Fatal("injected write failure was ignored")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("partially published output: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging residue=%v", entries)
	}
}
