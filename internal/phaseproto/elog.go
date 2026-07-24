package phaseproto

import (
	"fmt"
	"io"
	"log/syslog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ElogOptions struct {
	LogDir, Category, PF string
	Classes, Sinks       []string
	Output               io.Writer
	Now                  time.Time
}

var elogSummaryMu sync.Mutex

func DeliverElog(events []Event, options ElogOptions) ([]string, error) {
	if err := ValidateElogSinks(options.Sinks); err != nil {
		return nil, err
	}
	classes := make(map[string]bool)
	for _, class := range options.Classes {
		classes[strings.ToUpper(class)] = true
	}
	if len(classes) == 0 {
		for _, class := range []string{"INFO", "LOG", "WARN", "ERROR", "QA"} {
			classes[class] = true
		}
	}
	var selected []Event
	for _, event := range events {
		if event.Kind == "elog" && classes[event.Class] {
			selected = append(selected, event)
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	for _, sink := range options.Sinks {
		switch normalizeElogSink(sink) {
		case "echo", "save", "save-summary", "syslog":
		case "mail", "mail-summary", "custom":
			return nil, fmt.Errorf("phase elog: requested sink %q is unsupported", sink)
		default:
			return nil, fmt.Errorf("phase elog: unknown sink %q", sink)
		}
	}
	if options.Now.IsZero() {
		options.Now = time.Now()
	}
	var text strings.Builder
	for _, event := range selected {
		fmt.Fprintf(&text, "%s: %s\n", event.Class, event.Message)
	}
	content := text.String()
	var paths []string
	for _, sinkSpec := range options.Sinks {
		sink := normalizeElogSink(sinkSpec)
		switch sink {
		case "echo":
			if options.Output == nil {
				return paths, fmt.Errorf("phase elog: echo sink has no output")
			}
			if _, err := fmt.Fprintf(options.Output, "Messages for package %s/%s:\n%s", options.Category, options.PF, content); err != nil {
				return paths, fmt.Errorf("phase elog: echo: %w", err)
			}
		case "save":
			directory := filepath.Join(options.LogDir, "elog")
			if err := os.MkdirAll(directory, 0o2770); err != nil {
				return paths, fmt.Errorf("phase elog: create save directory: %w", err)
			}
			path := filepath.Join(directory, options.Category+":"+options.PF+":"+options.Now.UTC().Format("20060102-150405")+".log")
			file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o660)
			if err != nil {
				return paths, fmt.Errorf("phase elog: save: %w", err)
			}
			if _, err = io.WriteString(file, content); err == nil {
				err = file.Sync()
			}
			if closeErr := file.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return paths, fmt.Errorf("phase elog: save %s: %w", path, err)
			}
			paths = append(paths, path)
		case "save-summary":
			elogSummaryMu.Lock()
			path := filepath.Join(options.LogDir, "elog", "summary.log")
			err := os.MkdirAll(filepath.Dir(path), 0o2770)
			var file *os.File
			if err == nil {
				file, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o660)
			}
			if err == nil {
				_, err = fmt.Fprintf(file, ">>> Messages for package %s/%s on %s:\n%s\n", options.Category, options.PF, options.Now.Format("2006-01-02 15:04:05 MST"), content)
			}
			if err == nil {
				err = file.Sync()
			}
			if file != nil {
				if closeErr := file.Close(); err == nil {
					err = closeErr
				}
			}
			elogSummaryMu.Unlock()
			if err != nil {
				return paths, fmt.Errorf("phase elog: save-summary: %w", err)
			}
			paths = append(paths, path)
		case "syslog":
			writer, err := syslog.New(syslog.LOG_INFO|syslog.LOG_USER, "arise")
			if err != nil {
				return paths, fmt.Errorf("phase elog: syslog: %w", err)
			}
			for _, event := range selected {
				if err = writer.Info(fmt.Sprintf("%s/%s %s: %s", options.Category, options.PF, event.Class, event.Message)); err != nil {
					break
				}
			}
			if closeErr := writer.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return paths, fmt.Errorf("phase elog: syslog delivery: %w", err)
			}
		}
	}
	return paths, nil
}

func ValidateElogSinks(sinks []string) error {
	for _, sink := range sinks {
		switch normalizeElogSink(sink) {
		case "echo", "save", "save-summary", "syslog":
		case "mail", "mail-summary", "custom":
			return fmt.Errorf("phase elog: requested sink %q is unsupported", sink)
		default:
			return fmt.Errorf("phase elog: unknown sink %q", sink)
		}
	}
	return nil
}

func normalizeElogSink(spec string) string {
	name := strings.SplitN(spec, ":", 2)[0]
	return strings.ReplaceAll(name, "_", "-")
}
