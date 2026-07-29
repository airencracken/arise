package plancompare

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/airencracken/arise/internal/planvalidate"
)

const StateSchemaVersion = 1

type StateDocument struct {
	Schema  int                  `json:"schema"`
	Fixture planvalidate.Fixture `json:"fixture"`
	Plan    planvalidate.Plan    `json:"plan"`
}

type PolicyDocument struct {
	Schema int                  `json:"schema"`
	Policy ClassificationPolicy `json:"policy"`
}

func DecodeStateDocument(reader io.Reader) (StateAssessment, error) {
	var document StateDocument
	if err := decodeStrict(reader, &document); err != nil {
		return StateAssessment{}, fmt.Errorf("decode final-state assessment: %w", err)
	}
	if document.Schema != StateSchemaVersion {
		return StateAssessment{}, fmt.Errorf("decode final-state assessment: unsupported schema %d", document.Schema)
	}
	validation := planvalidate.ValidatePlanImpact(document.Fixture, document.Plan)
	applied := planvalidate.ApplyPlan(document.Fixture.Installed, document.Plan)
	return AssessmentFromValidation(validation, applied.State), nil
}

func DecodePolicyDocument(reader io.Reader) (ClassificationPolicy, error) {
	var document PolicyDocument
	if err := decodeStrict(reader, &document); err != nil {
		return ClassificationPolicy{}, fmt.Errorf("decode classification policy: %w", err)
	}
	if document.Schema != StateSchemaVersion {
		return ClassificationPolicy{}, fmt.Errorf("decode classification policy: unsupported schema %d", document.Schema)
	}
	return document.Policy, nil
}

func decodeStrict(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
