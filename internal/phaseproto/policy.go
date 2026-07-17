package phaseproto

import (
	"fmt"
	"os"
	"regexp"
	"strings"

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
	SourceDir     string
	ImageDir      string
	RootDir       string
	SysrootDir    string
	BrootDir      string
	TempDir       string
	HomeDir       string
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
	request.SourceDir = policy.SourceDir
	request.ImageDir = policy.ImageDir
	request.RootDir = policy.RootDir
	request.SysrootDir = policy.SysrootDir
	request.BrootDir = policy.BrootDir
	request.TempDir = policy.TempDir
	request.HomeDir = policy.HomeDir
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
	return request, request.Validate()
}
