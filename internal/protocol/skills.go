package protocol

import "time"

const (
	SkillSourceInjected   = "injected_by_clawmanager"
	SkillSourceDiscovered = "discovered_in_instance"
	SkillSourceBuiltin    = "builtin_in_openclaw"
)

type SkillInventoryItem struct {
	SkillID      string         `json:"skill_id,omitempty"`
	SkillVersion string         `json:"skill_version,omitempty"`
	Identifier   string         `json:"identifier"`
	InstallPath  string         `json:"install_path"`
	ContentMD5   string         `json:"content_md5"`
	Source       string         `json:"source"`
	Type         string         `json:"type"`
	SizeBytes    int64          `json:"size_bytes,omitempty"`
	FileCount    int            `json:"file_count,omitempty"`
	CollectedAt  time.Time      `json:"collected_at"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type SkillInventoryReportRequest struct {
	AgentID    string               `json:"agent_id"`
	ReportedAt time.Time            `json:"reported_at"`
	Mode       string               `json:"mode"`
	Trigger    string               `json:"trigger"`
	Skills     []SkillInventoryItem `json:"skills"`
}

type SkillUploadRequest struct {
	AgentID      string
	SkillID      string
	SkillVersion string
	Identifier   string
	ContentMD5   string
	Source       string
}
