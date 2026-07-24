package phaseproto

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/depstring"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/portage"
)

type PackagePolicy struct {
	Configuration *portage.Config
	Repositories  []portage.RepoEntry
	Repository    string
	ConfigRoot    string
	CPV           string
	Category      string
	PN            string
	P             string
	PR            string
	Slot          string
	WorkDir       string
	BuildDir      string
	SourceDir     string
	ImageDir      string
	RootDir       string
	SysrootDir    string
	BrootDir      string
	TempDir       string
	HomeDir       string
	LogFile       string
	Features      string
	Restrict      string
	Properties    string
	Use           map[string]bool
}

type ExecutionPolicy struct {
	Configured                                                    bool
	Sandbox, NetworkSandbox, IPCSandbox, PIDSandbox, MountSandbox bool
	UserPriv, UserSandbox, DropPrivileges, Tests, Fetch, Strip    bool
	Interactive                                                   bool
	Features, Restrict, Properties                                []string
}

func evaluatePolicyExpression(raw string, use map[string]bool) ([]string, error) {
	node, err := depstring.Parse(raw)
	if err != nil {
		return nil, err
	}
	var result []string
	var visit func(depstring.DepNode) error
	visit = func(current depstring.DepNode) error {
		switch value := current.(type) {
		case nil:
			return nil
		case *depstring.AtomDep:
			result = append(result, value.Atom)
			return nil
		case *depstring.AllOfGroup:
			for _, child := range value.Children {
				if err := visit(child); err != nil {
					return err
				}
			}
			return nil
		case *depstring.UseConditional:
			flag := strings.TrimPrefix(value.Flag, "!")
			enabled := use[flag]
			if strings.HasPrefix(value.Flag, "!") {
				enabled = !enabled
			}
			if enabled {
				for _, child := range value.Children {
					if err := visit(child); err != nil {
						return err
					}
				}
			}
			return nil
		default:
			return fmt.Errorf("unsupported policy group %T", current)
		}
	}
	if err := visit(node); err != nil {
		return nil, err
	}
	return result, nil
}

// EvaluatePolicyExpression resolves a metadata policy expression against the
// selected USE state. Fetch planning uses this before phase execution so it can
// honor RESTRICT=mirror and RESTRICT=primaryuri when ordering source candidates.
func EvaluatePolicyExpression(raw string, use map[string]bool) ([]string, error) {
	return evaluatePolicyExpression(raw, use)
}

func EvaluateExecutionPolicy(featureText, restrictText, propertyText string, use map[string]bool) (ExecutionPolicy, error) {
	policy := ExecutionPolicy{Configured: true, Fetch: true, Strip: true}
	for _, token := range strings.Fields(featureText) {
		enabled, name := true, token
		if strings.HasPrefix(name, "-") {
			enabled, name = false, strings.TrimPrefix(name, "-")
		}
		switch name {
		case "sandbox":
			policy.Sandbox = enabled
		case "network-sandbox":
			policy.NetworkSandbox = enabled
		case "ipc-sandbox":
			policy.IPCSandbox = enabled
		case "pid-sandbox":
			policy.PIDSandbox = enabled
		case "mount-sandbox":
			policy.MountSandbox = enabled
		case "userpriv":
			policy.UserPriv = enabled
		case "usersandbox":
			policy.UserSandbox = enabled
		case "test":
			policy.Tests = enabled
		case "nostrip":
			policy.Strip = !enabled
		case "split-log", "compress-build-logs", "fail-clean", "buildpkg", "collision-protect", "protect-owned", "preserve-libs", "xattr",
			"fixlafiles", "multilib-strict", "qa-unresolved-soname-deps", "strict", "strict-keepdir",
			"assume-digests", "binpkg-docompress", "binpkg-dostrip", "binpkg-logs", "binpkg-multi-instance", "buildpkg-live", "compress-index",
			"config-protect-if-modified", "distlocks", "ebuild-locks", "merge-sync", "merge-wait", "news", "parallel-fetch",
			"parallel-install", "pkgdir-index-trusted", "unknown-features-warn", "unmerge-logs", "unmerge-orphans", "userfetch", "usersync":
		default:
			if enabled {
				return policy, fmt.Errorf("unsupported enabled FEATURE %q", name)
			}
		}
	}
	restrict, err := evaluatePolicyExpression(restrictText, use)
	if err != nil {
		return policy, fmt.Errorf("RESTRICT: %w", err)
	}
	properties, err := evaluatePolicyExpression(propertyText, use)
	if err != nil {
		return policy, fmt.Errorf("PROPERTIES: %w", err)
	}
	policy.Restrict, policy.Properties, policy.Features = restrict, properties, strings.Fields(featureText)
	for _, name := range restrict {
		switch name {
		case "sandbox":
			policy.Sandbox = false
		case "network-sandbox":
			policy.NetworkSandbox = false
		case "userpriv":
			policy.UserPriv = false
			policy.DropPrivileges = false
		case "test":
			policy.Tests = false
		case "fetch":
			policy.Fetch = false
		case "strip":
			policy.Strip = false
		case "interactive":
			policy.Interactive = false
		case "mirror", "primaryuri", "bindist", "parallel", "binchecks":
		default:
			return policy, fmt.Errorf("unsupported enabled RESTRICT behavior %q", name)
		}
	}
	for _, name := range properties {
		switch name {
		case "interactive":
			policy.Interactive = true
			return policy, fmt.Errorf("unsupported enabled PROPERTY behavior %q", name)
		case "live", "virtual", "set":
			return policy, fmt.Errorf("unsupported enabled PROPERTY behavior %q", name)
		default:
			return policy, fmt.Errorf("unsupported enabled PROPERTY behavior %q", name)
		}
	}
	return policy, nil
}

var packageEnvironmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// packageEnvironmentReserved contains process-startup injection variables and
// paths owned by the execution protocol. package.env may configure toolchains,
// flags and package-specific variables, but may not replace the worker or its
// controlled directory contract.
var packageEnvironmentReserved = map[string]bool{
	"ARISE_ID": true, "ARISE_COMMAND": true, "ARISE_PHASE": true,
	"ARISE_EAPI": true, "ARISE_EBUILD": true, "ARISE_ECLASS_DIRS": true,
	"ARISE_USER_PATCH_DIRS": true, "BASH_ENV": true, "ENV": true,
	"SHELLOPTS": true, "PATH": true, "WORKDIR": true, "T": true,
	"S": true, "D": true, "ED": true, "ROOT": true, "SYSROOT": true,
	"BROOT": true, "HOME": true, "TMPDIR": true, "TMP": true, "TEMP": true,
	"PORTAGE_LOG_FILE": true, "PORTAGE_BUILDDIR": true,
	"PORTAGE_CONFIGROOT": true, "EBUILD_PHASE": true, "EBUILD_PHASE_FUNC": true,
}

var packageIdentityEnvironment = map[string]bool{
	"CATEGORY": true, "PN": true, "PV": true, "PR": true, "P": true,
	"PVR": true, "PF": true, "SLOT": true, "PORTAGE_REPO_NAME": true,
}

func init() {
	for name := range packageIdentityEnvironment {
		packageEnvironmentReserved[name] = true
	}
}

func packageIdentity(cpv, slot, repository string) (PackageIdentity, error) {
	category, pn, pvr, err := metadata.ParseCPV(cpv)
	if err != nil || pvr == "" {
		return PackageIdentity{}, fmt.Errorf("invalid package identity CPV %q", cpv)
	}
	version, err := atom.ParseVersion(pvr)
	if err != nil {
		return PackageIdentity{}, fmt.Errorf("invalid package identity version %q: %w", pvr, err)
	}
	pv, pr := pvr, "r0"
	if version.Revision >= 0 {
		suffix := fmt.Sprintf("-r%d", version.Revision)
		pv = strings.TrimSuffix(pvr, suffix)
		pr = strings.TrimPrefix(suffix, "-")
	}
	p := pn + "-" + pv
	pf := pn + "-" + pvr
	slot = strings.TrimSpace(slot)
	if slot == "" {
		slot = "0"
	}
	return PackageIdentity{Category: category, PN: pn, PV: pv, PR: pr, P: p, PVR: pvr, PF: pf, Slot: slot, Repository: repository}, nil
}

func mergePackageEnvironment(base, command map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(base)+len(command))
	for name, value := range base {
		if !packageEnvironmentName.MatchString(name) || strings.HasPrefix(name, "ARISE_") || packageEnvironmentReserved[name] {
			return nil, fmt.Errorf("phase policy: package.env may not set %q", name)
		}
		result[name] = value
	}
	// Explicit request/command environment is the final configuration layer.
	for name, value := range command {
		result[name] = value
	}
	return result, nil
}

// ApplyPackagePolicy derives execution paths from Portage package identity so
// callers do not hand-assemble eclass or user-patch precedence.
func ApplyPackagePolicy(request Request, policy PackagePolicy) (Request, error) {
	eclassDirs, err := portage.EclassLookupDirectories(policy.Repositories, policy.Repository)
	if err != nil {
		return request, fmt.Errorf("phase policy: eclass lookup: %w", err)
	}
	for _, directory := range eclassDirs {
		info, statErr := os.Stat(directory)
		if statErr != nil || !info.IsDir() {
			return request, fmt.Errorf("phase policy: eclass directory %s is unavailable", directory)
		}
	}
	var patchDirs []string
	for _, directory := range portage.UserPatchDirectories(policy.ConfigRoot, policy.Category, policy.PN, policy.P, policy.PR, policy.Slot) {
		info, statErr := os.Stat(directory)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil || !info.IsDir() {
			return request, fmt.Errorf("phase policy: user patch path %s is not a readable directory", directory)
		}
		patchDirs = append(patchDirs, directory)
	}
	request.EclassDirs = eclassDirs
	request.UserPatchDirs = patchDirs
	request.WorkDir = policy.WorkDir
	request.BuildDir = policy.BuildDir
	request.ConfigRoot = policy.ConfigRoot
	request.SourceDir = policy.SourceDir
	request.ImageDir = policy.ImageDir
	request.RootDir = policy.RootDir
	request.SysrootDir = policy.SysrootDir
	request.BrootDir = policy.BrootDir
	request.TempDir = policy.TempDir
	request.HomeDir = policy.HomeDir
	request.LogFile = policy.LogFile
	if policy.CPV != "" {
		identity, identityErr := packageIdentity(policy.CPV, policy.Slot, policy.Repository)
		if identityErr != nil {
			return request, fmt.Errorf("phase policy: %w", identityErr)
		}
		request.Package = identity
	}
	if policy.Configuration != nil {
		cpv := policy.CPV
		if cpv == "" && policy.Category != "" && policy.P != "" {
			cpv = policy.Category + "/" + policy.P
		}
		packageEnvironment, envErr := policy.Configuration.PackageExecutionEnvironmentFor(cpv, policy.Slot, policy.Repository, request.Env)
		if envErr != nil {
			return request, fmt.Errorf("phase policy: package environment: %w", envErr)
		}
		request.Env, envErr = mergePackageEnvironment(packageEnvironment, nil)
		if envErr != nil {
			return request, envErr
		}
	}
	// Portage always authorizes its package-owned scratch paths in sandbox,
	// independent of PORTAGE_TMPDIR. Relying on sandbox.conf's global /tmp
	// allowance breaks valid build roots on /var/tmp, /home or dedicated build
	// filesystems.
	writePaths := []string{policy.WorkDir, policy.BuildDir, policy.SourceDir, policy.ImageDir, policy.TempDir, policy.HomeDir}
	if policy.LogFile != "" {
		writePaths = append(writePaths, policy.LogFile)
	}
	if len(writePaths) != 0 {
		if request.Env == nil {
			request.Env = make(map[string]string)
		}
		seen := make(map[string]bool)
		var allowed []string
		for _, path := range strings.Split(request.Env["SANDBOX_WRITE"], ":") {
			if path = strings.TrimSpace(path); path != "" && !seen[path] {
				seen[path] = true
				allowed = append(allowed, path)
			}
		}
		for _, path := range writePaths {
			if path = strings.TrimSpace(path); path != "" && !seen[path] {
				seen[path] = true
				allowed = append(allowed, path)
			}
		}
		if len(allowed) != 0 {
			request.Env["SANDBOX_WRITE"] = strings.Join(allowed, ":")
		}
	}
	featuresText := policy.Features
	if request.Env["FEATURES"] != "" {
		featuresText = request.Env["FEATURES"]
	}
	if featuresText != "" || policy.Restrict != "" || policy.Properties != "" {
		executionPolicy, policyErr := EvaluateExecutionPolicy(featuresText, policy.Restrict, policy.Properties, policy.Use)
		if policyErr != nil {
			return request, fmt.Errorf("phase policy: %w", policyErr)
		}
		request.Policy = executionPolicy
	}
	return request, request.Validate()
}
