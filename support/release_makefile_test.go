package support

import (
	"os"
	"strings"
	"testing"
)

func TestDependencyArchiveCreatesDistDirectoryBeforeWriting(t *testing.T) {
	data, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	makefile := string(data)
	deps := strings.Index(makefile, "\ndeps:")
	mkdir := strings.Index(makefile, "\tmkdir -p dist\n")
	inputHash := strings.Index(makefile, "\tsha256sum go.mod go.sum > dist/.go-module-input.sha256\n")
	if deps < 0 || mkdir < deps || inputHash < mkdir {
		t.Fatalf("deps target must create dist before its first write")
	}
}
