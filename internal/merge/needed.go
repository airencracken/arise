package merge

import (
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// nativeNeededMetadata reads ELF dynamic sections without executing target
// binaries or invoking host tools such as ldd/readelf/scanelf.
func nativeNeededMetadata(imageDir string) (string, string, error) {
	type record struct {
		path, arch, soname, runpath, abi string
		needed                           []string
	}
	var records []record
	err := filepath.WalkDir(imageDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		raw, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open candidate ELF %s: %w", path, err)
		}
		var magic [4]byte
		_, magicErr := io.ReadFull(raw, magic[:])
		if magicErr != nil || string(magic[:]) != "\x7fELF" {
			raw.Close()
			return nil
		}
		if _, err := raw.Seek(0, io.SeekStart); err != nil {
			raw.Close()
			return fmt.Errorf("rewind candidate ELF %s: %w", path, err)
		}
		file, err := elf.NewFile(raw)
		if err != nil {
			raw.Close()
			var format *elf.FormatError
			if errors.As(err, &format) {
				return nil
			}
			return fmt.Errorf("inspect ELF %s: %w", path, err)
		}
		needed, err := file.ImportedLibraries()
		if err != nil {
			file.Close()
			return fmt.Errorf("read ELF dependencies %s: %w", path, err)
		}
		soname := firstDynamicString(file, elf.DT_SONAME)
		runpath := firstDynamicString(file, elf.DT_RUNPATH)
		if runpath == "" {
			runpath = firstDynamicString(file, elf.DT_RPATH)
		}
		relative, err := filepath.Rel(imageDir, path)
		if err != nil {
			return err
		}
		arch, abi := portageELFArch(file)
		if err := file.Close(); err != nil {
			return fmt.Errorf("close ELF %s: %w", path, err)
		}
		records = append(records, record{path: filepath.Join("/", relative), arch: arch, abi: abi, soname: soname, runpath: runpath, needed: needed})
		return nil
	})
	if err != nil {
		return "", "", err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].path < records[j].path })
	var legacy, elf2 []string
	for _, item := range records {
		deps := strings.Join(item.needed, ",")
		legacy = append(legacy, strings.TrimSpace(item.path+" "+deps))
		elf2 = append(elf2, strings.Join([]string{item.arch, item.path, item.soname, item.runpath, deps, item.abi}, ";"))
	}
	return strings.Join(legacy, "\n"), strings.Join(elf2, "\n"), nil
}

func firstDynamicString(file *elf.File, tag elf.DynTag) string {
	values, err := file.DynString(tag)
	if err != nil || len(values) == 0 {
		return ""
	}
	return values[0]
}

func portageELFArch(file *elf.File) (string, string) {
	switch file.Machine {
	case elf.EM_X86_64:
		return "X86_64", "x86_64"
	case elf.EM_386:
		return "X86", "x86"
	case elf.EM_AARCH64:
		return "AARCH64", "arm64"
	case elf.EM_ARM:
		return "ARM", "arm"
	case elf.EM_PPC64:
		return "PPC64", "ppc64"
	case elf.EM_RISCV:
		return "RISCV", "riscv"
	default:
		return file.Machine.String(), strings.ToLower(file.Machine.String())
	}
}
