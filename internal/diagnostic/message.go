// Package diagnostic carries human-readable explanations alongside wrapped
// errors. Error() remains available for logs and verbose technical output.
package diagnostic

import "errors"

type Message struct {
	Summary   string
	Context   []string
	Details   []string
	NextSteps []string
	LogFile   string
}

type Reporter interface {
	UserDiagnostic() Message
}

func Lookup(err error) (Message, bool) {
	var reporter Reporter
	if errors.As(err, &reporter) {
		return reporter.UserDiagnostic(), true
	}
	return Message{}, false
}
