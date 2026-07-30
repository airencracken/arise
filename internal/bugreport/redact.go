package bugreport

import (
	"net/url"
	"os"
	"os/user"
	"regexp"
	"sort"
	"strings"
)

type Redactor struct {
	literals []string
}

var secretValue = regexp.MustCompile(`(?i)(token|password|passwd|secret|authorization|cookie|proxy)([=: ]+)([^ \t\r\n]+)`)
var urlValue = regexp.MustCompile(`https?://[^ \t\r\n]+`)

func NewRedactor(extra ...string) *Redactor {
	literals := append([]string(nil), extra...)
	if home, err := os.UserHomeDir(); err == nil {
		literals = append(literals, home)
	}
	if current, err := user.Current(); err == nil {
		literals = append(literals, current.Username)
	}
	if hostname, err := os.Hostname(); err == nil {
		literals = append(literals, hostname)
	}
	sort.Slice(literals, func(i, j int) bool { return len(literals[i]) > len(literals[j]) })
	return &Redactor{literals: literals}
}

func (r *Redactor) String(value string) string {
	value = secretValue.ReplaceAllString(value, "$1$2[REDACTED]")
	value = urlValue.ReplaceAllStringFunc(value, redactURL)
	if r != nil {
		for _, literal := range r.literals {
			if len(literal) >= 2 {
				value = strings.ReplaceAll(value, literal, "[REDACTED]")
			}
		}
	}
	return value
}

func (r *Redactor) Strings(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = r.String(value)
	}
	return result
}

func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "[REDACTED-URL]"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
