package mergehistory

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeLog(t *testing.T, body string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "emerge.log")
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestReadPortageAndAriseHistory(t *testing.T) {
	log := `100: >>> emerge (1 of 2) dev-lang/go-1.27.3::overlay to /
101: >>> emerge (2 of 2) app-misc/client-1 to /
110: ::: completed emerge (2 of 2) app-misc/client-1 to /
120: ::: completed emerge (1 of 2) dev-lang/go-1.27.3::overlay to /
130: === Unmerging... (cat/old-1)
135: >>> unmerge success: cat/old-1
140: >>> unmerge (3 of 3) cat/other-2::gentoo to /
141: ::: completed unmerge (3 of 3) cat/other-2::gentoo to /
150: >>> Syncing repository 'gentoo' into '/var/db/repos/gentoo'...
151: >>> Starting rsync with rsync://mirror.example/gentoo-portage
155: === Sync completed for gentoo
160: >>> emerge (1 of 1) cat/failed-1 to /
165: *** exiting unsuccessfully.
170: *** terminating.
180: >>> emerge (1 of 1) cat/unfinished-1 to /
`
	report, err := Read(context.Background(), []string{writeLog(t, log)})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Events) != 7 {
		t.Fatalf("events=%#v", report.Events)
	}
	want := []string{"merge:completed", "merge:completed", "unmerge:completed", "unmerge:completed", "sync:completed", "merge:interrupted", "merge:incomplete"}
	for i, event := range report.Events {
		if event.Kind+":"+event.Status != want[i] {
			t.Errorf("event %d=%+v", i, event)
		}
	}
	if event := report.Events[0]; event.Repository != "overlay" || event.CPV != "dev-lang/go-1.27.3" || event.DurationSeconds == nil || *event.DurationSeconds != 20 {
		t.Fatalf("overlay event=%+v", event)
	}
	if report.Events[1].DurationSeconds == nil || *report.Events[1].DurationSeconds != 9 {
		t.Fatal("parallel completions were paired incorrectly")
	}
}

func TestReadRotatedCompressedAndOverlappingLogs(t *testing.T) {
	start := "100: >>> emerge (1 of 1) cat/pkg-1 to /\n"
	finish := "160: ::: completed emerge (1 of 1) cat/pkg-1 to /\n"
	plain := writeLog(t, start+finish)
	var gz bytes.Buffer
	writer := gzip.NewWriter(&gz)
	if _, err := writer.Write([]byte(start)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	compressed := filepath.Join(t.TempDir(), "emerge.log.1.gz")
	if err := os.WriteFile(compressed, gz.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bzip2", "-c")
	command.Stdin = strings.NewReader(finish)
	bz, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	bzip := filepath.Join(t.TempDir(), "emerge.log.2.bz2")
	if err := os.WriteFile(bzip, bz, 0600); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{{plain, compressed, bzip}, {bzip, compressed, plain}} {
		report, err := Read(context.Background(), paths)
		if err != nil || len(report.Events) != 1 || report.Events[0].DurationSeconds == nil || *report.Events[0].DurationSeconds != 60 {
			t.Fatalf("rotation report=%+v err=%v", report, err)
		}
	}
}

func TestHistoryCorruptionAndMissingTiming(t *testing.T) {
	log := "\x00\x00100: >>> emerge (1 of 1) cat/good-1 to /\n" +
		"160: ::: completed emerge (1 of 1) cat/good-1 to /\n" +
		"invalid: >>> emerge (1 of 1) cat/bad-1 to /\n" +
		"170: >>> emerge (1 of 1) invalid to /\n" +
		"180: ::: completed emerge (1 of 1) cat/orphan-1 to /\n" +
		"190: >>> emerge (1 of 1) cat/badtime-1 to /\n" +
		"185: ::: completed emerge (1 of 1) cat/badtime-1 to /\n"
	report, err := Read(context.Background(), []string{writeLog(t, log)})
	if err != nil {
		t.Fatal(err)
	}
	if report.RecoveredLines != 1 || report.MalformedLines != 2 {
		t.Fatalf("quality=%+v", report)
	}
	stats := Summarize(report.Events)
	for _, group := range stats {
		if group.Package != "cat/good" && group.Samples != 0 {
			t.Fatalf("invented timings for missing/reversed start: %+v", group)
		}
	}
}

func TestReadFailureDoesNotReturnPartialHistory(t *testing.T) {
	good := writeLog(t, "100: >>> emerge (1 of 1) cat/pkg-1 to /\n")
	for _, bad := range []string{filepath.Join(t.TempDir(), "absent"), writeLog(t, strings.Repeat("x", 4*1024*1024+1))} {
		report, err := Read(context.Background(), []string{good, bad})
		if err == nil || !reflect.DeepEqual(report, Report{}) {
			t.Fatalf("partial history accepted: %+v %v", report, err)
		}
	}
	corrupt := filepath.Join(t.TempDir(), "broken.gz")
	if err := os.WriteFile(corrupt, []byte("not gzip"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), []string{corrupt}); err == nil {
		t.Fatal("corrupt compressed log accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Read(ctx, []string{good}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestStatisticsMedianPredictionAndScope(t *testing.T) {
	report, err := Read(context.Background(), []string{writeLog(t, `100: >>> emerge (1 of 1) cat/pkg-1::repo to /
160: ::: completed emerge (1 of 1) cat/pkg-1::repo to /
200: >>> emerge (1 of 1) cat/pkg-2::repo to /
320: ::: completed emerge (1 of 1) cat/pkg-2::repo to /
400: >>> emerge (1 of 1) cat/pkg-2::other to /
900: ::: completed emerge (1 of 1) cat/pkg-2::other to /
1000: >>> emerge (1 of 1) cat/pkg-2::repo to /other/
1500: ::: completed emerge (1 of 1) cat/pkg-2::repo to /other/
1600: >>> emerge (1 of 1) cat/pkg-2::repo to /
1601: *** exiting unsuccessfully.
`)})
	if err != nil {
		t.Fatal(err)
	}
	stats := Summarize(report.Events)
	var selected Statistics
	for _, group := range stats {
		if group.Repository == "repo" && group.Root == "/" {
			selected = group
		}
	}
	if selected.Completed != 2 || selected.Incomplete != 1 || selected.MedianSeconds != 90 || selected.MeanSeconds != 90 || selected.P90Seconds != 120 || selected.TotalSeconds != 180 {
		t.Fatalf("stats=%+v", selected)
	}
	for _, query := range []struct {
		cpv, repo, root string
		want            float64
		basis           string
	}{{"cat/pkg-2", "repo", "/", 120, "same version"}, {"cat/pkg-3", "repo", "/", 90, "same package"}, {"cat/pkg-2", "other", "/", 500, "same version"}, {"cat/pkg-2", "repo", "/other", 500, "same version"}} {
		prediction, known := Predict(report.Events, "cat/pkg", query.cpv, query.repo, query.root)
		if !known || prediction.Seconds != query.want || prediction.Basis != query.basis {
			t.Fatalf("prediction=%+v known=%t", prediction, known)
		}
	}
	if _, known := Predict(report.Events, "cat/pkg", "cat/pkg-2", "absent", "/"); known {
		t.Fatal("mixed repository history")
	}
}

func FuzzHistoryRecordParser(f *testing.F) {
	for _, seed := range []string{"100: >>> emerge (1 of 1) cat/pkg-1 to /", "150: >>> unmerge success: cat/pkg-1", "100: >>> Syncing repository 'gentoo' into '/repo'...", "9999999999999999999999: ::: completed emerge (1 of 1) cat/pkg-1 to /"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		record, recognized, err := parseLine(input)
		if recognized && err == nil && !record.terminate && (record.key == "" || record.event.Package == "") {
			t.Fatal("accepted incomplete identity")
		}
	})
}

func TestMedianRetainsHalfSeconds(t *testing.T) {
	if got := median([]int64{1, 2}); got != 1.5 {
		t.Fatalf("even median=%v", got)
	}
}

func TestLegacyURLOnlySyncStillHasOneTiming(t *testing.T) {
	report, err := Read(context.Background(), []string{writeLog(t, "100: >>> starting rsync with rsync://mirror.example/gentoo\n160: === Sync completed with rsync://mirror.example/gentoo\n")})
	if err != nil || len(report.Events) != 1 || report.Events[0].DurationSeconds == nil || *report.Events[0].DurationSeconds != 60 {
		t.Fatalf("legacy sync=%+v, %v", report, err)
	}
}
