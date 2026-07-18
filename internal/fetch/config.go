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
	Destination   string
	DistfilesDir  string
	Timeout       time.Duration
	GentooMirrors []string
	MirrorGroups  map[string][]string
	Progress      func(Progress)
}

func (c *FetchConfig) defaults() {
	if c.Destination == "" {
		c.Destination = c.DistfilesDir
	}
	if c.Timeout == 0 {
		c.Timeout = 120 * time.Second
	}
}
