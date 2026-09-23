package phaseproto

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/portage"
)

// EvaluateMetadata sources an ebuild and its eclasses in the sandboxed worker
// without executing package phases. It does not consume make.conf, package.env,
// installed USE flags, or other machine-specific metadata inputs.
func EvaluateMetadata(ctx context.Context, source *metadata.PackageMetadata, repositories []portage.RepoEntry) (*metadata.PackageMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp("", "arise-metadata-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	request, err := ApplyPackagePolicy(Request{
		Protocol: Version, ID: "metadata", Command: "evaluate_metadata", EAPI: source.EAPI,
		Ebuild: filepath.Join(source.RepositoryPath, source.Category, source.Package, source.Package+"-"+source.Version+".ebuild"),
	}, PackagePolicy{
		Repositories: repositories, Repository: source.Repository, CPV: source.CPV(),
		WorkDir: scratch, TempDir: scratch, HomeDir: scratch, SourceDir: scratch,
		RootDir: "/", SysrootDir: "/", BrootDir: "/",
	})
	if err != nil {
		return nil, err
	}
	request.Policy = ExecutionPolicy{Configured: true, Sandbox: true, NetworkSandbox: true}
	events, err := RunBashWorker(ctx, request)
	if err != nil {
		var details []string
		for _, event := range events {
			if event.Kind == "log" || event.Kind == "elog" {
				details = append(details, event.Message)
			}
		}
		return nil, fmt.Errorf("evaluate %s::%s: %w: %s", source.CPV(), source.Repository, err, strings.Join(details, "; "))
	}
	return evaluatedMetadata(source, events)
}

func evaluatedMetadata(source *metadata.PackageMetadata, events []Event) (*metadata.PackageMetadata, error) {
	var cache strings.Builder
	for _, event := range events {
		if event.Kind != "metadata" {
			continue
		}
		fields := strings.Fields(event.Message)
		if event.Class == "DEFINED_PHASES" {
			for i, phase := range fields {
				fields[i] = strings.TrimPrefix(strings.TrimPrefix(phase, "src_"), "pkg_")
			}
			if len(fields) == 0 {
				fields = []string{"-"}
			}
		}
		fmt.Fprintf(&cache, "%s=%s\n", event.Class, strings.Join(fields, " "))
	}
	result, err := metadata.ParseCacheEntry(source.CPV(), []byte(cache.String()))
	if err != nil {
		return nil, err
	}
	if result.EAPI != source.EAPI || result.SLOT == "" {
		return nil, fmt.Errorf("evaluate %s::%s: missing SLOT or inconsistent EAPI", source.CPV(), source.Repository)
	}
	result.Repository, result.RepositoryPath = source.Repository, source.RepositoryPath
	result.RepositoryMasters = append([]string(nil), source.RepositoryMasters...)
	result.RepositoryPriority, result.OverlayIndex = source.RepositoryPriority, source.OverlayIndex
	result.EAPIBanned, result.EAPIDeprecated = source.EAPIBanned, source.EAPIDeprecated
	result.Maintainers = append([]string(nil), source.Maintainers...)
	result.MaintainerNeeded = source.MaintainerNeeded
	return result, nil
}
