package rescue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteScan stores a scan through the workspace so new writes receive a versioned identity index.
func WriteScan(path string, scan Scan) error {
	if filepath.Base(path) != "scan.json" {
		return writeJSONAtomic(path, scan)
	}
	workspace, err := OpenWorkspace(filepath.Dir(path))
	if err != nil {
		return err
	}
	return workspace.SaveScan(scan)
}

// ReadScan reads a scan record while preserving the original path-based compatibility interface.
func ReadScan(path string) (Scan, error) {
	var scan Scan
	err := readJSON(path, &scan)
	return scan, err
}

// WriteManifest atomically stores a manifest for callers that still use the path-based compatibility interface.
func WriteManifest(path string, manifest Manifest) error {
	return writeJSONAtomic(path, manifest)
}

// ReadManifest reads a manifest record from its compatibility path.
func ReadManifest(path string) (Manifest, error) {
	var manifest Manifest
	err := readJSON(path, &manifest)
	return manifest, err
}

// writeJSONAtomic replaces a JSON record only after the complete new value is durable in the same directory.
func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".forge-rescue-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath) // A failed replacement must not leave private records in stray files.

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// readJSON decodes one workspace record without exposing JSON handling to higher-level callers.
func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
