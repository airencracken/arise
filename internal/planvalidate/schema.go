package planvalidate

import (
	"encoding/json"
	"fmt"
	"io"
)

func DecodeFixture(reader io.Reader) (Fixture, error) {
	var fixture Fixture
	if err := decodeStrict(reader, &fixture); err != nil {
		return Fixture{}, fmt.Errorf("decode validation fixture: %w", err)
	}
	if fixture.Schema != SchemaVersion {
		return Fixture{}, fmt.Errorf("decode validation fixture: unsupported schema %d", fixture.Schema)
	}
	return fixture, nil
}

func DecodePlan(reader io.Reader) (Plan, error) {
	var plan Plan
	if err := decodeStrict(reader, &plan); err != nil {
		return Plan{}, fmt.Errorf("decode validation plan: %w", err)
	}
	if plan.Schema != SchemaVersion {
		return Plan{}, fmt.Errorf("decode validation plan: unsupported schema %d", plan.Schema)
	}
	return plan, nil
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
