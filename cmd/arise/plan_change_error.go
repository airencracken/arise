package main

import "github.com/airencracken/arise/internal/diagnostic"

type planChangeError struct {
	reason string
	resume bool
}

func (e *planChangeError) Error() string { return e.reason }

func (e *planChangeError) UserDiagnostic() diagnostic.Message {
	step := "Rerun the original command and review the newly calculated package plan."
	if e.resume {
		step = "Run 'arise --resume' to calculate and review a new plan for the remaining packages."
	}
	return diagnostic.Message{
		Summary: "The package plan must be recalculated before the next installation can proceed.",
		Context: []string{"Installed packages, package settings, or selected sources no longer match the approved plan."},
		Details: []string{e.reason}, NextSteps: []string{step},
	}
}
