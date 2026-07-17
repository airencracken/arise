package fetch

import "time"

type FetchConfig struct {
	Destination   string
	DistfilesDir  string
	Timeout       time.Duration
	GentooMirrors []string
	MirrorGroups  map[string][]string
}

func (c *FetchConfig) defaults() {
	if c.Destination == "" {
		c.Destination = c.DistfilesDir
	}
	if c.Timeout == 0 {
		c.Timeout = 120 * time.Second
	}
}
