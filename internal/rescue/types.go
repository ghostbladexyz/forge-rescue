package rescue

import (
	"encoding/json"
	"time"
)

const (
	RiskHigh   = "HIGH"
	RiskMedium = "MEDIUM"
	RiskSafe   = "SAFE"
)

type Repo struct {
	ID          int64      `json:"id,omitempty"`
	Name        string     `json:"name,omitempty"`
	FullName    string     `json:"full_name"`
	CloneURL    string     `json:"clone_url"`
	SSHURL      string     `json:"ssh_url,omitempty"`
	HTMLURL     string     `json:"html_url,omitempty"`
	Private     bool       `json:"private"`
	Fork        bool       `json:"fork"`
	Archived    bool       `json:"archived"`
	Size        int64      `json:"size"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	PushedAt    *time.Time `json:"pushed_at,omitempty"`
	Permissions any        `json:"permissions,omitempty"`
}

type Scan struct {
	Instance  string    `json:"instance"`
	ScannedAt time.Time `json:"scanned_at"`
	Repos     []Repo    `json:"repos"`
}

// RepositoryMetadata is one complete archival capture returned by the source forge before workspace persistence begins.
type RepositoryMetadata struct {
	Repository json.RawMessage
	Issues     []json.RawMessage
	Releases   []json.RawMessage
	Labels     []json.RawMessage
}

type Manifest struct {
	Instance   string    `json:"instance"`
	RescuedAt  time.Time `json:"rescued_at"`
	ReposTotal int       `json:"repos_total"`
	Success    int       `json:"success"`
	Failed     int       `json:"failed"`
	Failures   []Failure `json:"failures,omitempty"`
	Outcomes   []Outcome `json:"outcomes,omitempty"`
}

type Failure struct {
	Repo  string `json:"repo"`
	Error string `json:"error"`
}

// Outcome records enough phase detail to distinguish a resumable partial rescue from a complete rescue.
type Outcome struct {
	Repo             string `json:"repo"`
	Identity         string `json:"identity"`
	ArtifactKey      string `json:"artifact_key"`
	Status           string `json:"status"`
	MirrorComplete   bool   `json:"mirror_complete"`
	MetadataComplete bool   `json:"metadata_complete"`
	Error            string `json:"error,omitempty"`
}

const (
	OutcomeComplete = "complete"
	OutcomePartial  = "partial"
	OutcomeFailed   = "failed"
)

type RiskConfig struct {
	HighDays   int
	MediumDays int
}

type RiskResult struct {
	Level     string
	AgeDays   int
	CreatedAt time.Time
}

type Selection struct {
	Risk  string
	Names []string
}
