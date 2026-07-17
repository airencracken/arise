package fetch

import (
	"testing"
	"time"
)

func TestFetchConfigDefaults(t *testing.T) {
	config := FetchConfig{DistfilesDir: "/var/cache/distfiles"}
	config.defaults()
	if config.Destination != config.DistfilesDir || config.Timeout != 120*time.Second {
		t.Fatalf("defaults = %#v", config)
	}
}
