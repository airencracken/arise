package fetch

import "time"

type ProgressStage string

const (
	ProgressChecking  ProgressStage = "checking"
	ProgressCached    ProgressStage = "cached"
	ProgressDownload  ProgressStage = "download"
	ProgressVerifying ProgressStage = "verifying"
	ProgressComplete  ProgressStage = "complete"
)

type Progress struct {
	Stage      ProgressStage
	Artifact   string
	Source     string
	Downloaded int64
	Total      int64
}

type FetchConfig struct {
	Destination     string
	DistfilesDir    string
	Timeout         time.Duration
	GentooMirrors   []string
	MirrorGroups    map[string][]string
	RestrictMirrors bool
	PrimaryURI      bool
	// ManualOnly verifies existing DISTDIR entries but forbids every network
	// source. Callers use the typed ManualFetchRequiredError to decide whether
	// pkg_nofetch diagnostics are appropriate.
	ManualOnly bool
	Progress   func(Progress)
}

func (c *FetchConfig) defaults() {
	if c.Destination == "" {
		c.Destination = c.DistfilesDir
	}
	if c.Timeout == 0 {
		c.Timeout = 120 * time.Second
	}
}

// ApplyRestrictions applies the package's evaluated RESTRICT to either fetch path.
func (c *FetchConfig) ApplyRestrictions(restrict []string) {
	for _, name := range restrict {
		switch name {
		case "fetch":
			c.ManualOnly = true
		case "mirror":
			c.RestrictMirrors = true
		case "primaryuri":
			c.PrimaryURI = true
		}
	}
}
