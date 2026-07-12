// Package tables provides tables for the configstore
package tables

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// PromptDeploymentVersionRef is a single weighted pointer from a deployment to a
// concrete prompt version. The deployment's resolver splits live traffic across
// these refs proportionally to Weight (e.g. a 90/10 A/B split between two
// versions). VersionNumber is the immutable TablePromptVersion.VersionNumber it
// targets; if that version is later deleted the resolver drops this ref from the
// weighted pool and falls back to the remaining refs (or the prompt's latest
// version when none survive). Refs are stored serialized in
// TablePromptDeployment.VersionsJSON; they are not their own table.
type PromptDeploymentVersionRef struct {
	// VersionNumber is the targeted prompt version number (not the row ID).
	VersionNumber int `json:"version_number"`
	// Weight is the relative selection weight. Values <= 0 are treated as 0 and
	// never selected; at least one ref must carry a positive weight for the
	// deployment to route traffic.
	Weight int `json:"weight"`
}

// TablePromptDeployment is a named, weighted traffic-routing strategy for a
// single prompt. A prompt may have several deployments (e.g. "production",
// "staging"); each maps to a set of weighted version refs. At request time the
// prompts plugin's deployment resolver picks one version per request according
// to the weights, with a fallback to the prompt's latest version when a pinned
// version has been deleted. The version refs are stored as JSON (so changing the
// split needs no schema migration); the polymorphic owner FK to the prompt
// follows the cascade-delete pattern used elsewhere (see promptVersions.go).
type TablePromptDeployment struct {
	ID string `gorm:"primaryKey;type:varchar(255)" json:"id"`

	// PromptID is the owning prompt. A deployment is meaningless without its
	// prompt, so deleting the prompt cascades to its deployments.
	PromptID string       `gorm:"type:varchar(36);not null;index;uniqueIndex:idx_prompt_deployment_name,priority:1" json:"prompt_id"`
	Prompt   *TablePrompt `gorm:"foreignKey:PromptID;constraint:OnDelete:CASCADE" json:"prompt,omitempty"`

	// Name is the deployment label, unique per prompt (e.g. "production").
	Name string `gorm:"type:varchar(255);not null;uniqueIndex:idx_prompt_deployment_name,priority:2" json:"name"`

	// Enabled toggles whether the resolver considers this deployment. Disabled
	// deployments are ignored and traffic falls through to the latest version.
	Enabled bool `gorm:"not null;default:true;index" json:"enabled"`

	// VersionsJSON is the serialized []PromptDeploymentVersionRef. The runtime
	// Versions field is the source of truth on write (BeforeSave serializes it)
	// and is repopulated on read (AfterFind deserializes it).
	VersionsJSON string `gorm:"type:text" json:"-"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`

	// Versions is the runtime, non-persisted parsed ref list. Populated from
	// VersionsJSON by AfterFind and serialized back by BeforeSave.
	Versions []PromptDeploymentVersionRef `gorm:"-" json:"versions"`
}

// TableName sets the table name.
func (TablePromptDeployment) TableName() string { return "prompt_deployments" }

// BeforeSave validates the deployment and serializes the runtime Versions list
// into VersionsJSON.
func (d *TablePromptDeployment) BeforeSave(tx *gorm.DB) error {
	if strings.TrimSpace(d.PromptID) == "" {
		return fmt.Errorf("prompt deployment prompt_id cannot be empty")
	}
	if strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("prompt deployment name cannot be empty")
	}

	// Validate refs: weights cannot be negative and version numbers must be > 0.
	for _, ref := range d.Versions {
		if ref.VersionNumber <= 0 {
			return fmt.Errorf("prompt deployment version ref must reference a positive version number")
		}
		if ref.Weight < 0 {
			return fmt.Errorf("prompt deployment version ref weight cannot be negative")
		}
	}

	// Serialize the runtime list. A nil list persists as an empty array so reads
	// are always well-formed JSON.
	if d.Versions == nil {
		d.VersionsJSON = "[]"
	} else {
		data, err := json.Marshal(d.Versions)
		if err != nil {
			return fmt.Errorf("failed to serialize prompt deployment versions: %w", err)
		}
		d.VersionsJSON = string(data)
	}
	return nil
}

// AfterFind deserializes VersionsJSON back into the runtime Versions list.
func (d *TablePromptDeployment) AfterFind(tx *gorm.DB) error {
	if strings.TrimSpace(d.VersionsJSON) == "" {
		d.Versions = []PromptDeploymentVersionRef{}
		return nil
	}
	if err := json.Unmarshal([]byte(d.VersionsJSON), &d.Versions); err != nil {
		return fmt.Errorf("failed to deserialize prompt deployment versions: %w", err)
	}
	if d.Versions == nil {
		d.Versions = []PromptDeploymentVersionRef{}
	}
	return nil
}
