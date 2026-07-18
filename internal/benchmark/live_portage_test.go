//go:build live_portage

package benchmark

import "testing"

func TestCompareAtomSpeed(t *testing.T)     { liveCompareAtomSpeed(t) }
func TestCompareSearchSpeed(t *testing.T)   { liveCompareSearchSpeed(t) }
func TestCompareResolveSpeed(t *testing.T)  { liveCompareResolveSpeed(t) }
func TestCompareEqueryBelongs(t *testing.T) { liveCompareEqueryBelongs(t) }
func TestCompareEqueryFiles(t *testing.T)   { liveCompareEqueryFiles(t) }
