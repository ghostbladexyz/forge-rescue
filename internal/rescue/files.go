package rescue

import (
	"encoding/json"
	"os"
)

func WriteScan(path string, scan Scan) error {
	return writeJSON(path, scan)
}

func ReadScan(path string) (Scan, error) {
	var scan Scan
	err := readJSON(path, &scan)
	return scan, err
}

func WriteManifest(path string, manifest Manifest) error {
	return writeJSON(path, manifest)
}

func ReadManifest(path string) (Manifest, error) {
	var manifest Manifest
	err := readJSON(path, &manifest)
	return manifest, err
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
