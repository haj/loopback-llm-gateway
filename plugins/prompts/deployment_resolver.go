package prompts

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

const (
	// PromptDeploymentHeader selects which named deployment to route through when a
	// request does not pin an explicit version. Copied into BifrostContext by the
	// plugin's HTTPTransportPreHook, mirroring x-bf-prompt-id / x-bf-prompt-version.
	PromptDeploymentHeader = "x-bf-prompt-deployment"

	// PromptDeploymentKey is the context key for the resolved deployment name.
	PromptDeploymentKey schemas.BifrostContextKey = PromptDeploymentHeader
)

// DeploymentStore is the data source the deployment resolver needs on top of the
// version data it shares with the plugin: it lists enabled deployments so the
// resolver can build its in-memory routing table. The framework config store
// satisfies this alongside InMemoryStore.
type DeploymentStore interface {
	InMemoryStore
	GetActivePromptDeployments(ctx context.Context) ([]configstoreTables.TablePromptDeployment, error)
}

// DeploymentResolver wraps a base resolver (normally the header resolver) and,
// when a request does not pin an explicit version, routes it through a prompt's
// deployment: it picks one of the deployment's version refs at random, weighted
// by each ref's Weight. Refs whose target version has been deleted are dropped
// from the weighted pool; if no ref survives (or the deployment is empty), it
// falls back to the prompt's latest version (version number 0).
//
// The resolver keeps its own in-memory snapshot of enabled deployments and the
// set of valid version numbers per prompt, refreshed via reloadDeployments. The
// plugin's loadCache invokes that refresh so a single Reload (triggered by the
// HTTP handler after any prompt/version/deployment mutation) keeps both the
// plugin's version cache and this routing table coherent.
type DeploymentResolver struct {
	base   PromptResolver
	store  DeploymentStore
	logger schemas.Logger

	// pickWeighted selects an index in [0,total) given the cumulative weights.
	// Injectable for deterministic tests; defaults to a weighted random pick.
	pickWeighted func(total int) int

	mu sync.RWMutex
	// deploymentsByPrompt maps promptID -> deployment name -> deployment.
	deploymentsByPrompt map[string]map[string]*configstoreTables.TablePromptDeployment
	// enabledByPrompt lists the enabled deployments per prompt (for the
	// no-deployment-name single-deployment shortcut).
	enabledByPrompt map[string][]*configstoreTables.TablePromptDeployment
	// validVersions maps promptID -> set of existing version numbers, so the
	// resolver can drop refs that point at deleted versions.
	validVersions map[string]map[int]struct{}
}

// NewDeploymentResolver builds a deployment resolver. base is the fallback
// resolver used to obtain the prompt ID and any explicitly pinned version (nil
// falls back to the header resolver). store supplies deployments and versions.
func NewDeploymentResolver(base PromptResolver, store DeploymentStore, logger schemas.Logger) *DeploymentResolver {
	if base == nil {
		base = &headerResolver{logger: logger}
	}
	return &DeploymentResolver{
		base:                base,
		store:               store,
		logger:              logger,
		pickWeighted:        func(total int) int { return rand.IntN(total) },
		deploymentsByPrompt: make(map[string]map[string]*configstoreTables.TablePromptDeployment),
		enabledByPrompt:     make(map[string][]*configstoreTables.TablePromptDeployment),
		validVersions:       make(map[string]map[int]struct{}),
	}
}

// Resolve returns the prompt ID and version number to inject. It first defers to
// the base resolver. An explicitly pinned version (number > 0) always wins —
// deployments only steer requests that asked for the latest version. Otherwise,
// if a deployment applies to the prompt, a weighted version is selected with a
// latest-version fallback.
func (r *DeploymentResolver) Resolve(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (string, int, error) {
	promptID, versionNumber, err := r.base.Resolve(ctx, req)
	if err != nil {
		return "", 0, err
	}
	if promptID == "" {
		return "", 0, nil
	}
	// An explicit version pin bypasses deployment routing entirely.
	if versionNumber > 0 {
		return promptID, versionNumber, nil
	}

	deploymentName := strings.TrimSpace(bifrost.GetStringFromContext(ctx, PromptDeploymentKey))

	r.mu.RLock()
	defer r.mu.RUnlock()

	dep := r.selectDeployment(promptID, deploymentName)
	if dep == nil {
		// No applicable deployment: keep the base decision (latest version).
		return promptID, 0, nil
	}
	selected := r.weightedPick(promptID, dep)
	// selected == 0 means "fall back to latest version".
	return promptID, selected, nil
}

// selectDeployment picks the deployment to apply. If a name is supplied, only an
// enabled deployment with that exact name is used. With no name, the routing
// applies only when the prompt has exactly one enabled deployment (an
// unambiguous default); otherwise it returns nil so traffic stays on latest.
// Caller holds r.mu (read).
func (r *DeploymentResolver) selectDeployment(promptID, name string) *configstoreTables.TablePromptDeployment {
	if name != "" {
		if byName, ok := r.deploymentsByPrompt[promptID]; ok {
			if dep, ok := byName[name]; ok {
				return dep
			}
		}
		return nil
	}
	enabled := r.enabledByPrompt[promptID]
	if len(enabled) == 1 {
		return enabled[0]
	}
	return nil
}

// weightedPick selects a version number from the deployment's refs, weighted by
// each ref's Weight and excluding refs whose target version no longer exists.
// Returns 0 when no ref is selectable, signalling the latest-version fallback.
// Caller holds r.mu (read).
func (r *DeploymentResolver) weightedPick(promptID string, dep *configstoreTables.TablePromptDeployment) int {
	valid := r.validVersions[promptID]

	// Build the eligible pool: positive-weight refs that point at an existing
	// version. Track total weight for the weighted draw.
	type candidate struct {
		version int
		weight  int
	}
	candidates := make([]candidate, 0, len(dep.Versions))
	total := 0
	for _, ref := range dep.Versions {
		if ref.Weight <= 0 {
			continue
		}
		if valid != nil {
			if _, ok := valid[ref.VersionNumber]; !ok {
				// Pinned version was deleted; drop it from the pool (fallback path).
				continue
			}
		}
		candidates = append(candidates, candidate{version: ref.VersionNumber, weight: ref.Weight})
		total += ref.Weight
	}
	if total <= 0 || len(candidates) == 0 {
		if r.logger != nil {
			r.logger.Debug("prompts plugin: deployment %q for prompt %s has no selectable version; falling back to latest", dep.Name, promptID)
		}
		return 0
	}

	draw := r.pickWeighted(total)
	if draw < 0 || draw >= total {
		// Defensive: a misbehaving picker must not select an out-of-range version.
		draw = 0
	}
	for _, c := range candidates {
		if draw < c.weight {
			return c.version
		}
		draw -= c.weight
	}
	// Unreachable when total is computed correctly; fall back to latest.
	return 0
}

// reloadDeployments rebuilds the routing table from the store: the set of enabled
// deployments (indexed by prompt and name) and the set of valid version numbers
// per prompt. Invoked by the plugin's loadCache so a single Reload refreshes both
// the version cache and this table.
func (r *DeploymentResolver) reloadDeployments(ctx context.Context) error {
	if r.store == nil {
		return nil
	}

	deployments, err := r.store.GetActivePromptDeployments(ctx)
	if err != nil {
		return fmt.Errorf("loading active prompt deployments: %w", err)
	}
	versions, err := r.store.GetAllPromptVersions(ctx)
	if err != nil {
		return fmt.Errorf("loading prompt versions for deployment resolver: %w", err)
	}

	byPrompt := make(map[string]map[string]*configstoreTables.TablePromptDeployment)
	enabledByPrompt := make(map[string][]*configstoreTables.TablePromptDeployment)
	for i := range deployments {
		d := &deployments[i]
		if !d.Enabled {
			continue
		}
		if _, ok := byPrompt[d.PromptID]; !ok {
			byPrompt[d.PromptID] = make(map[string]*configstoreTables.TablePromptDeployment)
		}
		byPrompt[d.PromptID][d.Name] = d
		enabledByPrompt[d.PromptID] = append(enabledByPrompt[d.PromptID], d)
	}

	valid := make(map[string]map[int]struct{})
	for i := range versions {
		v := &versions[i]
		if _, ok := valid[v.PromptID]; !ok {
			valid[v.PromptID] = make(map[int]struct{})
		}
		valid[v.PromptID][v.VersionNumber] = struct{}{}
	}

	r.mu.Lock()
	r.deploymentsByPrompt = byPrompt
	r.enabledByPrompt = enabledByPrompt
	r.validVersions = valid
	r.mu.Unlock()
	return nil
}
