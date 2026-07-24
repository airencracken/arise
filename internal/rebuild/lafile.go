package rebuild

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	dependencyLibsLine = regexp.MustCompile(`(?m)^dependency_libs='([^']*)'$`)
	inheritedFlagsLine = regexp.MustCompile(`(?m)^inherited_linker_flags='([^']*)'$`)
	laThreadFlag       = regexp.MustCompile(`^(-mt|-mthreads|-kthread|-Kthread|-pthread|-pthreads|--thread-safe|-threads)`)
	laPkgconfigTwoUp   = regexp.MustCompile(`usr/lib[^/]*/pkgconfig/\.\./\.\.`)
	laPkgconfigOneUp   = regexp.MustCompile(`(usr/lib[^/]*)/pkgconfig/\.\.`)
)

func fixLaFiles(imageDir string) (int, []error, error) {
	fixed := 0
	var warnings []error
	err := filepath.WalkDir(imageDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".la") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rewritten, changed, err := rewriteLaFile(content)
		if err != nil {
			// Portage warns and skips malformed files. Preserve that behavior;
			// callers can surface the diagnostic without corrupting the image.
			warnings = append(warnings, fmt.Errorf("invalid libtool archive %s: %w", path, err))
			return nil
		}
		if changed {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, rewritten, info.Mode().Perm()); err != nil {
				return err
			}
			fixed++
		}
		return nil
	})
	return fixed, warnings, err
}

func rewriteLaFile(content []byte) ([]byte, bool, error) {
	depMatches := dependencyLibsLine.FindAllSubmatch(content, -1)
	if len(depMatches) != 1 {
		return nil, false, fmt.Errorf("expected one dependency_libs entry, found %d", len(depMatches))
	}
	inheritMatches := inheritedFlagsLine.FindAllSubmatch(content, -1)
	if len(inheritMatches) > 1 {
		return nil, false, fmt.Errorf("duplicated inherited_linker_flags entry")
	}
	depValue := string(depMatches[0][1])
	hasInherited := len(inheritMatches) == 1
	var inherited []string
	if hasInherited {
		inherited = strings.Fields(string(inheritMatches[0][1]))
	}
	var rpaths, libraryDirs, libraries []string
	for _, token := range strings.Fields(depValue) {
		switch {
		case strings.HasPrefix(token, "-l"):
			libraries = appendUnique(libraries, token)
		case strings.HasSuffix(token, ".la"):
			directory, base := filepath.Split(token)
			directory = strings.TrimSuffix(directory, string(filepath.Separator))
			if directory == "" || !strings.HasPrefix(base, "lib") {
				libraries = appendUnique(libraries, token)
			} else {
				libraries = appendUnique(libraries, "-l"+strings.TrimSuffix(strings.TrimPrefix(base, "lib"), ".la"))
				libraryDirs = appendUnique(libraryDirs, "-L"+directory)
			}
		case strings.HasPrefix(token, "-L"):
			token = strings.ReplaceAll(token, "X11R6/lib", "lib")
			token = strings.ReplaceAll(token, "local/lib", "lib")
			token = laPkgconfigTwoUp.ReplaceAllString(token, "usr")
			token = laPkgconfigOneUp.ReplaceAllString(token, "$1")
			libraryDirs = appendUnique(libraryDirs, token)
		case strings.HasPrefix(token, "-R"):
			rpaths = appendUnique(rpaths, token)
		case laThreadFlag.MatchString(token):
			if hasInherited {
				inherited = appendUnique(inherited, token)
			} else {
				libraries = appendUnique(libraries, token)
			}
		default:
			return nil, false, fmt.Errorf("unexpected dependency_libs entry %q", token)
		}
	}
	expectedDependency := prefixedFields(append(append(rpaths, libraryDirs...), libraries...))
	expectedInherited := prefixedFields(inherited)
	result := append([]byte(nil), content...)
	changed := false
	if depValue != expectedDependency {
		result = bytes.Replace(result, []byte("dependency_libs='"+depValue+"'"), []byte("dependency_libs='"+expectedDependency+"'"), 1)
		changed = true
	}
	if hasInherited {
		old := string(inheritMatches[0][1])
		if old != expectedInherited {
			result = bytes.Replace(result, []byte("inherited_linker_flags='"+old+"'"), []byte("inherited_linker_flags='"+expectedInherited+"'"), 1)
			changed = true
		}
	}
	return result, changed, nil
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func prefixedFields(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return " " + strings.Join(values, " ")
}
