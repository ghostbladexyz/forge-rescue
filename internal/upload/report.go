package upload

import (
	"encoding/json"
	"os"
	"time"
)

type Report struct {
	Provider   string    `json:"provider"`
	Owner      string    `json:"owner"`
	UploadedAt time.Time `json:"uploaded_at"`
	ReposTotal int       `json:"repos_total"`
	Success    int       `json:"success"`
	Failed     int       `json:"failed"`
	Skipped    int       `json:"skipped"`
	Failures   []Failure `json:"failures,omitempty"`
}

type Failure struct {
	Repo  string `json:"repo"`
	Error string `json:"error"`
}

func WriteReport(path string, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func ReadReport(path string) (Report, error) {
	var report Report
	data, err := os.ReadFile(path)
	if err != nil {
		return report, err
	}
	err = json.Unmarshal(data, &report)
	return report, err
}
