package store

import "encoding/json"

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Codebase is one folder of source code a person inspects.
type Codebase struct {
	ID          int64  `json:"id"`
	UserID      int64  `json:"-"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`   // path | git | upload | files
	Location    string `json:"location"` // the folder, clone URL, archive name or "space:path"
	Branch      string `json:"branch"`
	Subdir      string `json:"subdir"`
	GitUser     string `json:"git_user"`
	GitToken    string `json:"-"`
	HasToken    bool   `json:"has_token"`
	Vendored    bool   `json:"vendored"`
	RescanHours int    `json:"rescan_hours"`
	Pinned      bool   `json:"pinned"`
	LastScanID  *int64 `json:"last_scan_id"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`

	// Filled in for lists: the latest scan (any status) and the latest finished one.
	Latest *Scan `json:"latest,omitempty"`
	Done   *Scan `json:"done,omitempty"`

	// How the person asking may use it: "owner", or the role it is shared
	// with them in (then Owner is whose it is); Shares is with how many
	// people the owner shares it.
	Role   string `json:"role"`
	Owner  string `json:"owner,omitempty"`
	Shares int    `json:"shares"`
}

// Share roles.
const (
	ShareView = "view" // read every scan, keep one's own notes, ask the AI
	ShareScan = "scan" // also scan it again
)

// Share is one person a codebase is shared with.
type Share struct {
	UserID    int64  `json:"user_id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

// Person is someone on this box one may share with.
type Person struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Scan is one reading of a codebase. The result itself is loaded separately.
type Scan struct {
	ID          int64           `json:"id"`
	UserID      int64           `json:"-"`
	CodebaseID  int64           `json:"codebase_id"`
	Status      string          `json:"status"` // queued | running | done | failed | cancelled
	Stage       string          `json:"stage"`
	Progress    int             `json:"progress"`
	Error       string          `json:"error"`
	Trigger     string          `json:"trigger"`
	Commit      string          `json:"commit"`
	Branch      string          `json:"branch"`
	Stats       json.RawMessage `json:"stats"`
	ResultBytes int64           `json:"result_bytes"`
	StartedAt   string          `json:"started_at"`
	FinishedAt  string          `json:"finished_at"`
	DurationMS  int64           `json:"duration_ms"`
	CreatedAt   string          `json:"created_at"`
}

// Note is a person's note, or an AI explanation, on part of a codebase.
type Note struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"-"`
	CodebaseID int64  `json:"codebase_id"`
	Path       string `json:"path"`
	Kind       string `json:"kind"` // note | ai
	Text       string `json:"text"`
	ScanID     *int64 `json:"scan_id"`
	Model      string `json:"model"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// Conn is how a person reaches one sibling Pocket app.
type Conn struct {
	Mode      string `json:"mode"` // core | direct | off | "" (not chosen)
	URL       string `json:"url"`
	Token     string `json:"token"`
	PublicURL string `json:"public_url"`
}

// Settings are the box-wide values plus one person's preferences and their
// own connections. Connection modes: "core" (an app link through Pocket
// Core), "direct" (URL + token) or "off"; "" = not chosen yet.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`

	// Scanning.
	KeepScans    int    `json:"keep_scans"`    // finished scans kept per codebase
	Vendored     bool   `json:"vendored"`      // default for new codebases
	GitCommits   int    `json:"git_commits"`   // history read for churn (0 = no history)
	HideTests    bool   `json:"hide_tests"`    // leave test files out of the graph
	GraphLayout  string `json:"graph_layout"`  // layers | organic
	ShowExternal bool   `json:"show_external"` // outside packages in the file graph

	// Notifications.
	NotifyDone   bool   `json:"notify_done"`
	NotifyFailed bool   `json:"notify_failed"`
	NotifyURL    string `json:"notify_url"`
	NotifyFormat string `json:"notify_format"` // apprise | ntfy | json

	// Sibling Pocket apps, by slug.
	Conns map[string]Conn `json:"conns"`

	// AI for explanations and questions: Pocket AI through Core, or an
	// OpenAI-compatible API with the person's own key.
	AIProvider    string `json:"ai_provider"` // auto | pocket | openai
	AIPocketModel string `json:"ai_pocket_model"`
	OpenAIKey     string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"`
}

// Conn returns a person's connection to an app (zero value if none).
func (st Settings) Conn(slug string) Conn {
	if st.Conns == nil {
		return Conn{}
	}
	return st.Conns[slug]
}

// SetConn stores a person's connection to an app.
func (st *Settings) SetConn(slug string, c Conn) {
	if st.Conns == nil {
		st.Conns = map[string]Conn{}
	}
	st.Conns[slug] = c
}
