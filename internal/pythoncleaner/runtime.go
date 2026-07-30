package pythoncleaner

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/vdb"
)

type RuntimeProbe struct {
	CPV         string `json:"cpv"`
	Target      string `json:"target"`
	Interpreter string `json:"interpreter"`
	Module      string `json:"module"`
	Evidence    string `json:"evidence"`
}

type ProbeFailure struct {
	Probe  RuntimeProbe `json:"probe"`
	Detail string       `json:"detail"`
}

var extensionModuleRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)(?:\.(?:cpython-[^.]+|abi3))?\.so$`)
var modulePartRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// BuildRuntimeProbes derives side-effect-bounded native extension imports from
// VDB ownership. Pure Python modules are not imported because package imports
// may execute arbitrary initialization code.
func BuildRuntimeProbes(vdbRoot, root string, targets, repairedTargets []string) ([]RuntimeProbe, error) {
	packages, err := vdb.Scan(vdbRoot)
	if err != nil {
		return nil, err
	}
	repaired := map[string]bool{}
	for _, raw := range repairedTargets {
		parsed, err := atom.Parse(raw)
		if err != nil || parsed.Category == "" || parsed.Package == "" {
			return nil, fmt.Errorf("python-cleaner: invalid repaired target %q", raw)
		}
		repaired[parsed.CP()] = true
	}
	targetSet := map[string]bool{}
	for _, target := range targets {
		targetSet[target] = true
	}
	seen := map[string]bool{}
	var probes []RuntimeProbe
	for _, pkg := range packages {
		if !repaired[pkg.CP()] {
			continue
		}
		scanner := bufio.NewScanner(strings.NewReader(pkg.Contents))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 || fields[0] != "obj" {
				continue
			}
			target, module, ok := nativeModuleFromPath(fields[1])
			if !ok || !targetSet[target] {
				continue
			}
			interpreter := confinedInterpreter(root, target)
			key := target + "\x00" + module
			if seen[key] {
				continue
			}
			seen[key] = true
			probes = append(probes, RuntimeProbe{
				CPV: pkg.CPV(), Target: target, Interpreter: interpreter,
				Module: module, Evidence: filepath.Clean(fields[1]),
			})
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	sort.Slice(probes, func(i, j int) bool {
		if probes[i].Target != probes[j].Target {
			return probes[i].Target < probes[j].Target
		}
		if probes[i].Module != probes[j].Module {
			return probes[i].Module < probes[j].Module
		}
		return probes[i].CPV < probes[j].CPV
	})
	return probes, nil
}

func RunRuntimeProbes(ctx context.Context, probes []RuntimeProbe, timeout time.Duration) []ProbeFailure {
	var failures []ProbeFailure
	for _, probe := range probes {
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		command := exec.CommandContext(
			probeCtx, probe.Interpreter, "-I", "-c",
			"import importlib,sys; importlib.import_module(sys.argv[1])",
			probe.Module,
		)
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		command.Cancel = func() error {
			if command.Process == nil {
				return nil
			}
			err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			if err == syscall.ESRCH {
				return os.ErrProcessDone
			}
			return err
		}
		command.WaitDelay = time.Second
		command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "LANG=C"}
		var output bytes.Buffer
		command.Stdout, command.Stderr = &output, &output
		err := command.Run()
		cancel()
		if err == nil {
			continue
		}
		detail := strings.TrimSpace(output.String())
		if probeCtx.Err() == context.DeadlineExceeded {
			detail = "probe timed out"
		} else if detail == "" {
			detail = err.Error()
		}
		if len(detail) > 4096 {
			detail = detail[:4096] + "..."
		}
		failures = append(failures, ProbeFailure{Probe: probe, Detail: detail})
	}
	return failures
}

func nativeModuleFromPath(path string) (string, string, bool) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(clean), "/"), "/")
	if len(parts) < 3 || parts[0] != "usr" ||
		(parts[1] != "lib" && parts[1] != "lib32" && parts[1] != "lib64" && parts[1] != "libx32") {
		return "", "", false
	}
	siteIndex := -1
	target := ""
	for index, part := range parts {
		normalized := normalizeTarget(part)
		if normalized != "" && index+1 < len(parts) && parts[index+1] == "site-packages" {
			target, siteIndex = normalized, index+1
			break
		}
	}
	if siteIndex < 0 || siteIndex+1 >= len(parts) {
		return "", "", false
	}
	relative := append([]string(nil), parts[siteIndex+1:]...)
	match := extensionModuleRE.FindStringSubmatch(relative[len(relative)-1])
	if match == nil {
		return "", "", false
	}
	relative[len(relative)-1] = match[1]
	if relative[len(relative)-1] == "__init__" {
		relative = relative[:len(relative)-1]
	}
	if len(relative) == 0 {
		return "", "", false
	}
	for _, part := range relative {
		if !modulePartRE.MatchString(part) {
			return "", "", false
		}
	}
	return target, strings.Join(relative, "."), true
}

func confinedInterpreter(root, target string) string {
	name := strings.ReplaceAll(target, "_", ".")
	if filepath.Clean(root) == "/" {
		return filepath.Join("/usr/bin", name)
	}
	return filepath.Join(filepath.Clean(root), "usr", "bin", name)
}

func InterpreterSmokeProbes(root string, targets []string) []RuntimeProbe {
	result := make([]RuntimeProbe, 0, len(targets))
	for _, target := range targets {
		result = append(result, RuntimeProbe{
			Target: target, Interpreter: confinedInterpreter(root, target),
			Module: "sys", Evidence: "policy interpreter smoke probe",
		})
	}
	return result
}
