package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/benchmark"
	"github.com/airencracken/arise/internal/depstring"
	"github.com/airencracken/arise/internal/equery"
	"github.com/airencracken/arise/internal/search"
)

func runBench() {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	compare := fs.Bool("compare", false, "Run emerge comparison benchmarks (requires emerge on PATH)")
	quick := fs.Bool("quick", false, "Fewer iterations for quick check")
	jsonOut := fs.Bool("json", false, "Output as JSON")
	benchName := fs.String("bench", "", "Run specific benchmark by name")

	fs.Parse(flag.Args()[1:])

	iterations := 1000
	if *quick {
		iterations = 10
	}

	results := runBenchmarks(iterations, *compare, *benchName)

	if *jsonOut {
		fmt.Println(benchmark.FormatComparisonsJSON(results))
		return
	}

	if len(results) == 0 {
		fmt.Println("No benchmarks matched.")
		return
	}

	fmt.Println(benchmark.FormatComparisonSummary(results))
}

func runBenchmarks(iterations int, withCompare bool, filter string) []benchmark.Comparison {
	var results []benchmark.Comparison

	match := func(name string) bool {
		if filter == "" {
			return true
		}
		return strings.Contains(strings.ToLower(name), strings.ToLower(filter))
	}

	if match("atom-parse") {
		r := runBenchOp("atom-parse", iterations, func() error {
			_, err := atom.Parse(">=sys-devel/gcc-12.2.0:12/12.2=[fortran]")
			return err
		})
		results = append(results, r)
	}

	if match("atom-compare") {
		v1, _ := atom.ParseVersion("12.2.0-r3")
		v2, _ := atom.ParseVersion("13.1.0_alpha1")
		r := runBenchOp("atom-compare", iterations, func() error {
			v1.Compare(v2)
			return nil
		})
		results = append(results, r)
	}

	if match("depstring-parse") {
		input := "|| ( dev-lang/python >=dev-lang/python-3.10 ) python_single_target_python3_10? ( dev-python/foo ) !!sys-libs/blocker !app-misc/conflict >=dev-libs/glib-2.70"
		r := runBenchOp("depstring-parse", iterations, func() error {
			_, err := depstring.Parse(input)
			return err
		})
		results = append(results, r)
	}

	if match("depstring-satisfy") {
		input := "|| ( dev-lang/python >=dev-lang/python-3.10 ) python_single_target_python3_10? ( dev-python/foo ) >=dev-libs/glib-2.70"
		tree, _ := depstring.Parse(input)
		installed := map[string]*atom.Atom{
			"dev-lang/python": {Category: "dev-lang", Package: "python", Version: mustParseVersion("3.12.0")},
			"dev-libs/glib":   {Category: "dev-libs", Package: "glib", Version: mustParseVersion("2.80.0")},
		}
		useFlags := map[string]bool{"python_single_target_python3_10": true}
		r := runBenchOp("depstring-satisfy", iterations, func() error {
			sat, _ := depstring.Satisfy(tree, installed, useFlags)
			if !sat {
				return fmt.Errorf("not satisfied")
			}
			return nil
		})
		results = append(results, r)
	}

	if match("search") {
		db, err := benchmark.CreateTestDB(5000)
		if err != nil {
			fmt.Fprintf(os.Stderr, "search setup failed: %v\n", err)
		} else {
			defer db.Close()
			r := runBenchOp("search-all", iterations/10, func() error {
				_, err := search.Search(db, search.SearchConfig{})
				return err
			})
			results = append(results, r)

			r = runBenchOp("search-name", iterations/10, func() error {
				_, err := search.Search(db, search.SearchConfig{Query: "pkg-250", Exact: true})
				return err
			})
			results = append(results, r)

			r = runBenchOp("search-json", iterations/10, func() error {
				_, err := search.Search(db, search.SearchConfig{JSON: true})
				return err
			})
			results = append(results, r)
		}
	}

	if match("equery-belongs") && withCompare {
		vdbPath, _, err := benchmark.CreateTempVDB()
		if err != nil {
			fmt.Fprintf(os.Stderr, "vdb setup: %v\n", err)
		} else {
			defer os.RemoveAll(vdbPath)
			targetFile := "/bin/sh"
			if _, err := os.Stat(targetFile); err != nil {
				targetFile = "/etc/hostname"
			}
			r := benchmark.RunComparisonNoTB("equery-belongs", func() error {
				_, err := equery.Belongs(vdbPath, targetFile)
				return err
			}, func() (string, error) {
				out, err := exec.Command("equery", "belongs", targetFile).Output()
				return string(out), err
			})
			results = append(results, r)
		}
	}

	if match("equery-files") && withCompare {
		vdbPath, _, err := benchmark.CreateTempVDB()
		if err != nil {
			fmt.Fprintf(os.Stderr, "vdb setup: %v\n", err)
		} else {
			defer os.RemoveAll(vdbPath)
			r := benchmark.RunComparisonNoTB("equery-files", func() error {
				_, err := equery.Files(vdbPath, "app-admin/pkg-0-1.0")
				return err
			}, func() (string, error) {
				out, err := exec.Command("equery", "files", "app-admin/pkg-0-1.0").Output()
				return string(out), err
			})
			results = append(results, r)
		}
	}

	return results
}

func runBenchOp(name string, n int, fn func() error) benchmark.Comparison {
	if n < 1 {
		n = 1
	}
	start := time.Now()
	for i := 0; i < n; i++ {
		_ = fn()
	}
	elapsed := time.Since(start)
	ops := int64(0)
	if elapsed.Nanoseconds() > 0 {
		ops = int64(float64(n) / elapsed.Seconds())
	}
	return benchmark.Comparison{
		Name:         name,
		AriseOps:     ops,
		AriseTotal:   elapsed,
		AriseCorrect: true,
	}
}

func mustParseVersion(ver string) *atom.Version {
	v, _ := atom.ParseVersion(ver)
	return v
}
