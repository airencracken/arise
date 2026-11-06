package rebuild

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/airencracken/arise/internal/phaseproto"
	"github.com/dsnet/compress/bzip2"
)

func savedProtocolEnvironment(request phaseproto.Request) ([]byte, error) {
	environment := protocolEnvironmentSnapshot(request)
	if request.EnvironmentOverlay == "" {
		return environment, nil
	}
	state, err := os.ReadFile(request.EnvironmentOverlay)
	if err != nil {
		return nil, fmt.Errorf("read final phase environment: %w", err)
	}
	// Frame the current format before the typed configuration header, so
	// marker-like text inside a package value cannot select a legacy decoder.
	header, _, err := phaseproto.ParseSavedEnvironmentHeader(state)
	if err != nil {
		return nil, fmt.Errorf("read final phase environment metadata: %w", err)
	}
	framed := append([]byte(nil), header...)
	framed = append(framed, environment...)
	return append(framed, state...), nil
}

// Installation has already committed when pkg_postinst finishes. Atomically
// replace its environment so later removal hooks see that phase's final state.
func saveInstalledProtocolEnvironment(vdbPath string, request phaseproto.Request) error {
	environment, err := savedProtocolEnvironment(request)
	if err != nil {
		return err
	}
	output, err := os.CreateTemp(vdbPath, ".environment.bz2.tmp-*")
	if err != nil {
		return fmt.Errorf("create installed phase environment: %w", err)
	}
	defer os.Remove(output.Name())
	defer output.Close()
	writer, err := bzip2.NewWriter(output, nil)
	if err != nil {
		return err
	}
	if _, err := writer.Write(environment); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := output.Chmod(0644); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := os.Rename(output.Name(), filepath.Join(vdbPath, "environment.bz2")); err != nil {
		return fmt.Errorf("publish installed phase environment: %w", err)
	}
	directory, err := os.Open(vdbPath)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
