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
	want := `USE="firmware -ibm -systemd" PYTHON_SINGLE_TARGET="python3_14 -python3_12 -python3_13"`
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
	want := `USE="firmware python_single_target_python3_14 -systemd -python_single_target_python3_13"`
	if got != want {
		t.Fatalf("fallback installed USE display = %q, want %q", got, want)
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
	if !strings.Contains(version, "\x1b[44m") || !strings.Contains(version, "\x1b[92m") {
		t.Fatalf("installed version lacks contrasting foreground/background: %q", version)
	}
}
