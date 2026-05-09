package rescue

import "time"

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

type Manifest struct {
	Instance   string    `json:"instance"`
	RescuedAt  time.Time `json:"rescued_at"`
	ReposTotal int       `json:"repos_total"`
	Success    int       `json:"success"`
	Failed     int       `json:"failed"`
	Failures   []Failure `json:"failures,omitempty"`
}

type Failure struct {
	Repo  string `json:"repo"`
	Error string `json:"error"`
}

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
