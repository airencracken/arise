package support

import (
	"encoding/xml"
	"os"
	"strings"
	"testing"
)

type overlayRepositories struct {
	Version string              `xml:"version,attr"`
	Repos   []overlayRepository `xml:"repo"`
}

type overlayRepository struct {
	Quality     string `xml:"quality,attr"`
	Status      string `xml:"status,attr"`
	Name        string `xml:"name"`
	Description string `xml:"description"`
	Homepage    string `xml:"homepage"`
	Owner       struct {
		Type  string `xml:"type,attr"`
		Email string `xml:"email"`
		Name  string `xml:"name"`
	} `xml:"owner"`
	Sources []struct {
		Type string `xml:"type,attr"`
		URL  string `xml:",chardata"`
	} `xml:"source"`
	Feed string `xml:"feed"`
}

func TestOverlayListingCandidateContract(t *testing.T) {
	data, err := os.ReadFile("../misc/arise-overlay-repositories.xml")
	if err != nil {
		t.Fatal(err)
	}
	var document overlayRepositories
	if err := xml.Unmarshal(data, &document); err != nil {
		t.Fatalf("candidate XML is malformed: %v", err)
	}
	if document.Version != "1.0" || len(document.Repos) != 1 {
		t.Fatalf("candidate envelope = version %q, repos %d", document.Version, len(document.Repos))
	}
	repo := document.Repos[0]
	if repo.Quality != "experimental" || repo.Status != "unofficial" || repo.Name != "arise-overlay" {
		t.Fatalf("candidate classification = %#v", repo)
	}
	if repo.Description == "" || repo.Homepage != "https://github.com/airencracken/arise-overlay" {
		t.Fatalf("candidate public identity = %#v", repo)
	}
	if repo.Owner.Type != "person" || repo.Owner.Email == "" ||
		strings.HasSuffix(repo.Owner.Email, "@users.noreply.github.com") ||
		repo.Owner.Name != "Marcus Hildum" {
		t.Fatalf("candidate owner = %#v", repo.Owner)
	}
	if len(repo.Sources) != 2 || repo.Sources[0].Type != "git" ||
		repo.Sources[0].URL != "https://github.com/airencracken/arise-overlay.git" ||
		repo.Sources[1].Type != "git" ||
		repo.Sources[1].URL != "git+ssh://git@github.com/airencracken/arise-overlay.git" {
		t.Fatalf("candidate sources = %#v", repo.Sources)
	}
	if repo.Feed != "https://github.com/airencracken/arise-overlay/commits/master.atom" {
		t.Fatalf("candidate feed = %q", repo.Feed)
	}
}

func TestOverlayListingReadinessRouteExists(t *testing.T) {
	data, err := os.ReadFile("../docs/planning/OVERLAY_LISTING_READINESS.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"pkgcheck scan",
		"Bugzilla",
		"make check",
		"repositories: add arise-overlay",
		"Do not prepare the Gentoo fork",
	} {
		if !strings.Contains(string(data), required) {
			t.Errorf("readiness guide is missing %q", required)
		}
	}
}
