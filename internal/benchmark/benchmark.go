package benchmark

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/metadata"
	"github.com/dgraph-io/badger/v4"
)

type Comparison struct {
	Name         string
	AriseOps     int64
	EmergeOps    int64
	AriseTotal   time.Duration
	EmergeTotal  time.Duration
	AriseCorrect bool
	Speedup      float64
}

func RunComparison(t testing.TB, name string, ariseFn func() error, emergeFn func() (string, error)) Comparison {
	_ = t
	return runComparisonN(name, 10, ariseFn, emergeFn)
}

// RunComparisonN makes repetition count explicit for expensive external-tool
// comparisons. Live commands must first be measured once before choosing n.
func RunComparisonN(t testing.TB, name string, n int, ariseFn func() error, emergeFn func() (string, error)) Comparison {
	_ = t
	return runComparisonN(name, n, ariseFn, emergeFn)
}

func runComparisonN(name string, n int, ariseFn func() error, emergeFn func() (string, error)) Comparison {
	c := Comparison{Name: name, AriseCorrect: true}
	if n <= 0 {
		return c
	}
	ariseStart := time.Now()
	for i := 0; i < n; i++ {
		if err := ariseFn(); err != nil {
			c.AriseCorrect = false
		}
	}
	c.AriseTotal = time.Since(ariseStart)
	if c.AriseTotal.Nanoseconds() > 0 {
		c.AriseOps = int64(float64(n) / c.AriseTotal.Seconds())
	}

	if emergeFn == nil {
		return c
	}

	var emergeOut string
	emergeStart := time.Now()
	for i := 0; i < n; i++ {
		out, err := emergeFn()
		if err != nil {
			c.AriseCorrect = false
		}
		emergeOut = out
	}
	c.EmergeTotal = time.Since(emergeStart)
	if c.EmergeTotal.Nanoseconds() > 0 {
		c.EmergeOps = int64(float64(n) / c.EmergeTotal.Seconds())
	}
	_ = emergeOut

	if c.AriseTotal > 0 && c.EmergeTotal > 0 {
		c.Speedup = float64(c.EmergeTotal) / float64(c.AriseTotal)
	} else if c.AriseTotal > 0 && emergeFn == nil {
		c.Speedup = math.Inf(1)
	}
	return c
}

func RunComparisonNoTB(name string, ariseFn func() error, emergeFn func() (string, error)) Comparison {
	return runComparisonN(name, 10, ariseFn, emergeFn)
}

func FormatComparison(c Comparison) string {
	spd := "-"
	if c.Speedup > 0 && !math.IsInf(c.Speedup, 0) {
		spd = fmt.Sprintf("%.2fx", c.Speedup)
	}
	correct := "no"
	if c.AriseCorrect {
		correct = "yes"
	}
	return fmt.Sprintf("  %-30s %12d ops/s %12d ops/s %7s %8s",
		c.Name, c.AriseOps, c.EmergeOps, correct, spd)
}

func FormatComparisonSummary(comparisons []Comparison) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%-30s %14s %14s %7s %8s\n", "Benchmark", "Arise ops/s", "Emerge ops/s", "Correct", "Speedup"))
	b.WriteString("-------------------------------------------------------------------------------\n")
	for _, c := range comparisons {
		b.WriteString(FormatComparison(c))
		b.WriteString("\n")
	}
	return b.String()
}

func FormatComparisonsJSON(comparisons []Comparison) string {
	type jsonComp struct {
		Name         string  `json:"name"`
		AriseOps     int64   `json:"arise_ops_per_sec"`
		EmergeOps    int64   `json:"emerge_ops_per_sec"`
		AriseTotal   string  `json:"arise_total"`
		EmergeTotal  string  `json:"emerge_total"`
		AriseCorrect bool    `json:"arise_correct"`
		Speedup      float64 `json:"speedup"`
	}
	entries := make([]jsonComp, len(comparisons))
	for i, c := range comparisons {
		entries[i] = jsonComp{
			Name:         c.Name,
			AriseOps:     c.AriseOps,
			EmergeOps:    c.EmergeOps,
			AriseTotal:   c.AriseTotal.String(),
			EmergeTotal:  c.EmergeTotal.String(),
			AriseCorrect: c.AriseCorrect,
			Speedup:      c.Speedup,
		}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	return string(data)
}

func CreateTestDB(n int) (*badger.DB, error) {
	opts := badger.DefaultOptions("").WithInMemory(true).WithLoggingLevel(badger.ERROR)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open test db: %w", err)
	}
	categories := []string{
		"app-admin", "dev-libs", "sys-apps", "net-misc", "x11-libs",
		"media-libs", "app-text", "dev-util", "sci-libs", "net-libs",
	}
	wb := db.NewWriteBatch()
	for i := 0; i < n; i++ {
		cat := categories[i%len(categories)]
		pkg := fmt.Sprintf("pkg-%d", i)
		m := &metadata.PackageMetadata{
			Category:    cat,
			Package:     pkg,
			Version:     "1.0",
			SLOT:        "0",
			DESCRIPTION: fmt.Sprintf("Test package number %d", i),
			KEYWORDS:    "amd64",
			IUSE:        "foo bar baz",
			LICENSE:     "GPL-2",
			EAPI:        "8",
		}
		if i < n-1 {
			var deps []string
			for j := 1; j <= 3 && i+j < n; j++ {
				depIdx := i + j
				depCat := categories[depIdx%len(categories)]
				deps = append(deps, fmt.Sprintf("%s/pkg-%d", depCat, depIdx))
			}
			if len(deps) > 0 {
				m.DEPEND = strings.Join(deps, " ")
			}
		}
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(m); err != nil {
			wb.Cancel()
			db.Close()
			return nil, fmt.Errorf("encode: %w", err)
		}
		if err := wb.Set([]byte("pkg:"+m.RepositoryCPVKey()), buf.Bytes()); err != nil {
			wb.Cancel()
			db.Close()
			return nil, fmt.Errorf("set: %w", err)
		}
	}
	if err := wb.Flush(); err != nil {
		db.Close()
		return nil, fmt.Errorf("flush: %w", err)
	}
	return db, nil
}

func CreateTempVDB() (string, []string, error) {
	vdbPath, err := os.MkdirTemp("", "arise-bench-vdb-")
	if err != nil {
		return "", nil, fmt.Errorf("mktemp vdb: %w", err)
	}
	catDir := filepath.Join(vdbPath, "app-admin")
	if err := os.MkdirAll(catDir, 0755); err != nil {
		os.RemoveAll(vdbPath)
		return "", nil, fmt.Errorf("mkdir vdb category: %w", err)
	}
	pvDir := filepath.Join(catDir, "pkg-1.0")
	if err := os.MkdirAll(pvDir, 0755); err != nil {
		os.RemoveAll(vdbPath)
		return "", nil, fmt.Errorf("mkdir pv dir: %w", err)
	}

	candidates := []string{"/bin/ls", "/etc/hostname", "/etc/os-release"}
	refFiles := make([]string, 0, 2)
	for _, candidate := range candidates {
		info, statErr := os.Lstat(candidate)
		if statErr == nil && info.Mode().IsRegular() {
			refFiles = append(refFiles, candidate)
		}
		if len(refFiles) == 2 {
			break
		}
	}

	var contents []string
	for _, rf := range refFiles {
		if _, err := os.Stat(rf); err == nil {
			contents = append(contents, fmt.Sprintf("obj %s 0000 0", rf))
		}
	}
	if len(contents) == 0 {
		tmpF, err := os.CreateTemp(vdbPath, "reference-")
		if err == nil {
			if _, writeErr := tmpF.WriteString("benchmark data\n"); writeErr == nil {
				contents = append(contents, fmt.Sprintf("obj %s 15 0", tmpF.Name()))
			}
			_ = tmpF.Close()
		}
	}
	contentsStr := strings.Join(contents, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(pvDir, "CONTENTS"), []byte(contentsStr), 0644); err != nil {
		os.RemoveAll(vdbPath)
		return "", nil, fmt.Errorf("write CONTENTS: %w", err)
	}
	for _, f := range []string{"CATEGORY", "PF", "USE", "EAPI"} {
		val := ""
		switch f {
		case "CATEGORY":
			val = "app-admin"
		case "PF":
			val = "pkg-1.0"
		case "USE":
			val = "foo bar -baz"
		case "EAPI":
			val = "8"
		}
		if err := os.WriteFile(filepath.Join(pvDir, f), []byte(val+"\n"), 0644); err != nil {
			os.RemoveAll(vdbPath)
			return "", nil, fmt.Errorf("write %s: %w", f, err)
		}
	}

	return vdbPath, refFiles, nil
}

func ExtractAllFromDB(db *badger.DB) ([]*metadata.PackageMetadata, error) {
	var pkgs []*metadata.PackageMetadata
	err := db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		prefix := []byte("pkg:")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				buf := bytes.NewBuffer(val)
				dec := gob.NewDecoder(buf)
				var m metadata.PackageMetadata
				if decodeErr := dec.Decode(&m); decodeErr != nil {
					return decodeErr
				}
				pkgs = append(pkgs, &m)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("extract from db: %w", err)
	}
	return pkgs, nil
}
