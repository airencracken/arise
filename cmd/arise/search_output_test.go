package main

import (
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/search"
)

func TestInstalledUseDisplayGroupsUseExpandState(t *testing.T) {
	old := color.UseColor
	color.UseColor = false
	t.Cleanup(func() { color.UseColor = old })

	installed := search.InstalledVersion{
		EnabledUSE: []string{"firmware", "python_single_target_python3_14", "abi_x86_64", "kernel_linux"},
		DisabledUSE: []string{
			"-ibm", "-systemd", "-python_single_target_python3_12",
			"-python_single_target_python3_13", "-abi_x86_x32",
		},
	}
	got := installedUseDisplay(
		installed,
		[]string{"PYTHON_SINGLE_TARGET", "ABI_X86", "KERNEL", "PYTHON_SINGLE_TARGET"},
		[]string{"ABI_X86", "KERNEL"},
	)
	want := `(firmware -ibm -systemd PYTHON_SINGLE_TARGET="python3_14 -python3_12 -python3_13")`
	if got != want {
		t.Fatalf("installed USE display = %q, want %q", got, want)
	}
}

func TestInstalledUseDisplayRemainsMeaningfulWithoutUseExpandConfiguration(t *testing.T) {
	old := color.UseColor
	color.UseColor = false
	t.Cleanup(func() { color.UseColor = old })

	installed := search.InstalledVersion{
		EnabledUSE:  []string{"firmware", "python_single_target_python3_14"},
		DisabledUSE: []string{"-systemd", "-python_single_target_python3_13"},
	}
	got := installedUseDisplay(installed, nil, nil)
	want := `(firmware python_single_target_python3_14 -systemd -python_single_target_python3_13)`
	if got != want {
		t.Fatalf("fallback installed USE display = %q, want %q", got, want)
	}
}

func TestDeclaredUseDisplayMatchesEixGrouping(t *testing.T) {
	old := color.UseColor
	color.UseColor = false
	t.Cleanup(func() { color.UseColor = old })

	got := declaredUseDisplay(
		"pi4 pi5 +python_targets_python3_12 python_targets_python3_13 abi_x86_64",
		[]string{"PYTHON_TARGETS", "ABI_X86"},
		[]string{"ABI_X86"},
	)
	want := `{pi4 pi5 abi_x86_64 PYTHON_TARGETS="+python3_12 python3_13"}`
	if got != want {
		t.Fatalf("declared USE display = %q, want %q", got, want)
	}
}

func TestDeclaredUseDisplayColorsOnlyEixBraces(t *testing.T) {
	old := color.UseColor
	color.UseColor = true
	t.Cleanup(func() { color.UseColor = old })

	got := declaredUseDisplay("+cmsis-dap ftdi", nil, nil)
	want := "\x1b[1m\x1b[33m{\x1b[0m+cmsis-dap ftdi\x1b[1m\x1b[33m}\x1b[0m"
	if got != want {
		t.Fatalf("colored declared USE display = %q, want %q", got, want)
	}
}

func TestAvailableVersionDisplayMatchesEixKeywordMarkers(t *testing.T) {
	old := color.UseColor
	color.UseColor = false
	t.Cleanup(func() { color.UseColor = old })

	tests := []struct {
		name     string
		keywords string
		want     string
	}{
		{name: "stable", keywords: "amd64 arm64", want: "1"},
		{name: "testing", keywords: "~amd64 arm64", want: "~1"},
		{name: "missing stable keyword", keywords: "-* arm arm64", want: "*1"},
		{name: "missing testing keyword", keywords: "-* ~arm ~arm64", want: "~*1"},
		{name: "unkeyworded", want: "**1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := availableVersionDisplay(search.VersionInfo{Version: "1", Keywords: test.keywords}, "amd64", nil)
			if got != test.want {
				t.Fatalf("available version = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAvailableVersionDisplayMarksInstalledVersion(t *testing.T) {
	old := color.UseColor
	color.UseColor = true
	t.Cleanup(func() { color.UseColor = old })

	got := availableVersionDisplay(
		search.VersionInfo{Version: "3.0.81.2", Slot: "0", Keywords: "amd64"},
		"amd64",
		[]search.InstalledVersion{{Version: "3.0.81.2"}},
	)
	if !strings.Contains(got, "\x1b[32m") || !strings.Contains(got, "\x1b[7m") {
		t.Fatalf("installed available version lacks stable and inverse styles: %q", got)
	}
}

func TestInstalledSearchStylesAreVisuallyDistinct(t *testing.T) {
	old := color.UseColor
	color.UseColor = true
	t.Cleanup(func() { color.UseColor = old })

	marker := color.InstalledMarker("I")
	version := color.InstalledVersion("4.3.17-r4")
	for name, value := range map[string]string{"marker": marker, "version": version} {
		if !strings.Contains(value, "\x1b[") || !strings.Contains(value, "\x1b[7m") && name == "marker" {
			t.Fatalf("%s lacks installed styling: %q", name, value)
		}
	}
	if !strings.Contains(version, "\x1b[44m") || !strings.Contains(version, "\x1b[30m") {
		t.Fatalf("installed version does not match eix's black-on-blue styling: %q", version)
	}
}
