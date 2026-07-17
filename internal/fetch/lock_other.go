//go:build !linux

package fetch

import (
	"context"
	"fmt"
)

func acquireArtifactLock(context.Context, string, string) (func(), error) {
	return nil, fmt.Errorf("fetch: cross-process DISTDIR locking is unsupported on this platform")
}
