package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/downloadprepare"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

// NodeAwarePreparer keeps artifact queue ownership central while executing the
// expensive FFmpeg process on a healthy transcode node when capacity permits.
// The node retains completed bytes behind an authenticated opaque-id endpoint;
// integrated installations and unavailable pools fall back to local work.
type NodeAwarePreparer struct {
	local        EncodePreparer
	planner      nodepool.TranscodeWorkPlanner
	liveCfg      func() *config.Config
	remote       downloadprepare.RemotePreparer
	originLookup artifactOriginLookup
	settings     SettingsReader
	capabilityMu sync.Mutex
	capabilities map[string]remoteToneMapCapabilities
}

// remoteToneMapCapabilities caches one node's validated inventory; an empty
// slice with a short expiry represents a recent lookup failure.
type remoteToneMapCapabilities struct {
	capabilities tonemap.Capabilities
	expiresAt    time.Time
}

const (
	remoteToneMapCapabilityTTL      = time.Minute
	remoteToneMapCapabilityErrorTTL = 15 * time.Second
	remoteToneMapCapabilityTimeout  = 5 * time.Second
)

// eligibleTranscodeWorkPlanner reserves work only on nodes that satisfy a
// lock-safe capability predicate.
type eligibleTranscodeWorkPlanner interface {
	ReserveTranscodeWorkWith(workID string, eligible func(*nodepool.Node) bool) (*nodepool.Node, func())
}

// transcodeNodeEnumerator lists the currently enabled transcode pool for
// concurrent capability discovery.
type transcodeNodeEnumerator interface {
	TranscodeNodeURLs() []string
}

type artifactOriginLookup interface {
	GetByID(ctx context.Context, id int) (*nodepool.Node, error)
}

// NewNodeAwarePreparer creates a preparer that can select local or pooled execution.
func NewNodeAwarePreparer(local EncodePreparer, planner nodepool.TranscodeWorkPlanner, liveCfg func() *config.Config) *NodeAwarePreparer {
	if local == nil {
		local = playbackPreparer{}
	}
	return &NodeAwarePreparer{
		local:        local,
		planner:      planner,
		liveCfg:      liveCfg,
		remote:       downloadprepare.HTTPPreparer{},
		capabilities: make(map[string]remoteToneMapCapabilities),
	}
}

// SetOriginLookup supplies the authoritative node record used when the active
// pool temporarily misses an enabled node, and to recover a changed URL after
// a disabled node has left that pool.
func (p *NodeAwarePreparer) SetOriginLookup(lookup artifactOriginLookup) {
	p.originLookup = lookup
}

// SetSettingsReader supplies the live local-fallback policy. It is wired by
// ArtifactManager together with the tone-map settings used to freeze recipes.
func (p *NodeAwarePreparer) SetSettingsReader(settings SettingsReader) {
	p.settings = settings
}

// LocalFallbackAllowed reports whether this pooled preparer may execute on
// the API host when no compatible node is available.
func (p *NodeAwarePreparer) LocalFallbackAllowed(ctx context.Context) bool {
	if p == nil || p.planner == nil || p.settings == nil {
		return true
	}
	values, err := p.settings.GetAll(ctx)
	if err != nil {
		return true
	}
	return !strings.EqualFold(values[config.PlaybackLocalTranscodeFallbackSettingKey], "false")
}

// prepareLocally enforces the live local-fallback policy before delegating to
// the API host's artifact preparer.
func (p *NodeAwarePreparer) prepareLocally(ctx context.Context, artifactID string, opts playback.TranscodeOpts, outputPath string) (PreparedArtifact, error) {
	if !p.LocalFallbackAllowed(ctx) {
		return PreparedArtifact{}, errors.New("no eligible transcode node and local transcode fallback is disabled")
	}
	return p.local.PrepareFile(ctx, artifactID, opts, outputPath)
}

// PrepareFile routes an artifact job to a compatible node or allowed local fallback.
func (p *NodeAwarePreparer) PrepareFile(ctx context.Context, artifactID string, opts playback.TranscodeOpts, outputPath string) (PreparedArtifact, error) {
	cfg := p.config()
	jwtSecret := ""
	if cfg != nil {
		jwtSecret = strings.TrimSpace(cfg.Auth.JWTSecret)
	}
	if cfg == nil || jwtSecret == "" || p.remote == nil || p.planner == nil || !downloadprepare.ValidArtifactID(artifactID) {
		return p.prepareLocally(ctx, artifactID, opts, outputPath)
	}
	var node *nodepool.Node
	var release func()
	if opts.ToneMapMode != "" {
		selector, ok := p.planner.(eligibleTranscodeWorkPlanner)
		if ok {
			capable := p.capableToneMapNodeURLs(ctx, opts.ToneMapMode, opts.ToneMapSourceKind)
			node, release = selector.ReserveTranscodeWorkWith("download-prepare-"+artifactID, func(candidate *nodepool.Node) bool {
				_, supported := capable[strings.TrimRight(candidate.URL, "/")]
				return supported
			})
		}
	} else {
		node, release = p.planner.ReserveTranscodeWork("download-prepare-" + artifactID)
	}
	if node == nil {
		return p.prepareLocally(ctx, artifactID, opts, outputPath)
	}

	slog.InfoContext(ctx, "dispatching download artifact prepare", "component", "downloads", "artifact_id", artifactID, "node", node.URL)
	result, err := p.remote.Prepare(ctx, node.URL, jwtSecret, downloadprepare.NewRequest(artifactID, opts))
	release()
	if err == nil && result.ArtifactID == artifactID {
		return remotePreparedArtifact(node, result), nil
	}
	if err == nil {
		err = fmt.Errorf("remote download prepare returned artifact id %q, want %q", result.ArtifactID, artifactID)
	}
	if ctx.Err() != nil {
		return remotePreparedArtifact(node, downloadprepare.Result{ArtifactID: artifactID}), ctx.Err()
	}
	// A completed encode can outlive a lost HTTP response. Probe the same opaque
	// id before falling back so retry/recovery does not duplicate expensive work.
	if recovered, statErr := p.remote.Stat(ctx, node.URL, jwtSecret, artifactID); statErr == nil && recovered.ArtifactID == artifactID {
		slog.InfoContext(ctx, "recovered completed download artifact after lost response", "component", "downloads", "artifact_id", artifactID, "node", node.URL)
		return remotePreparedArtifact(node, recovered), nil
	} else if statErr == nil {
		return remotePreparedArtifact(node, downloadprepare.Result{ArtifactID: artifactID}),
			fmt.Errorf("remote download artifact recovery returned artifact id %q, want %q", recovered.ArtifactID, artifactID)
	} else if !errors.Is(statErr, downloadprepare.ErrArtifactNotFound) {
		slog.WarnContext(ctx, "remote download artifact recovery probe failed", "component", "downloads", "artifact_id", artifactID, "node", node.URL, "error", statErr)
		// The POST may have completed even though its response was lost. If the
		// follow-up probe is also indeterminate, retry the same opaque id later
		// instead of falling back locally and orphaning completed remote bytes.
		return remotePreparedArtifact(node, downloadprepare.Result{ArtifactID: artifactID}), errors.Join(err, fmt.Errorf("remote download artifact recovery probe: %w", statErr))
	}
	if ctx.Err() != nil {
		return PreparedArtifact{}, ctx.Err()
	}
	slog.WarnContext(ctx, "remote download artifact prepare unavailable; falling back to local", "component", "downloads", "artifact_id", artifactID, "node", node.URL, "error", err)
	return p.prepareLocally(ctx, artifactID, opts, outputPath)
}

// ToneMapCapabilities reports the validated executor union of enabled pooled
// transcode nodes. Selection rechecks the same per-node records before
// reserving work, so heterogeneous pools cannot receive an incompatible job.
func (p *NodeAwarePreparer) ToneMapCapabilities(ctx context.Context) tonemap.Capabilities {
	result := tonemap.Capabilities{}
	for _, capabilities := range p.toneMapCapabilitiesByNode(ctx) {
		result = append(result, capabilities...)
	}
	return result
}

// capableToneMapNodeURLs returns the normalized URLs of nodes that validated
// the exact mode and source kind frozen in an artifact recipe.
func (p *NodeAwarePreparer) capableToneMapNodeURLs(ctx context.Context, mode tonemap.Mode, kind tonemap.SourceKind) map[string]struct{} {
	result := make(map[string]struct{})
	for nodeURL, capabilities := range p.toneMapCapabilitiesByNode(ctx) {
		if capabilities.Supports(mode, kind) {
			result[nodeURL] = struct{}{}
		}
	}
	return result
}

// toneMapCapabilitiesByNode fetches the enabled pool concurrently and keeps
// each inventory attached to its node for safe heterogeneous selection.
func (p *NodeAwarePreparer) toneMapCapabilitiesByNode(ctx context.Context) map[string]tonemap.Capabilities {
	enumerator, ok := p.planner.(transcodeNodeEnumerator)
	if !ok {
		return map[string]tonemap.Capabilities{}
	}
	nodeURLs := enumerator.TranscodeNodeURLs()
	results := make([]tonemap.Capabilities, len(nodeURLs))
	var wg sync.WaitGroup
	for i, nodeURL := range nodeURLs {
		wg.Add(1)
		go func(i int, nodeURL string) {
			defer wg.Done()
			capabilities, err := p.toneMapCapabilitiesForNode(ctx, nodeURL)
			if err == nil {
				results[i] = capabilities
			}
		}(i, nodeURL)
	}
	wg.Wait()
	byNode := make(map[string]tonemap.Capabilities, len(nodeURLs))
	for i, capabilities := range results {
		if capabilities != nil {
			byNode[strings.TrimRight(nodeURLs[i], "/")] = capabilities
		}
	}
	return byNode
}

// toneMapCapabilitiesForNode returns a defensive copy of a fresh cached
// inventory or retrieves the node's authenticated hardware capabilities.
func (p *NodeAwarePreparer) toneMapCapabilitiesForNode(ctx context.Context, nodeURL string) (tonemap.Capabilities, error) {
	nodeURL = strings.TrimRight(nodeURL, "/")
	now := time.Now()
	p.capabilityMu.Lock()
	entry, ok := p.capabilities[nodeURL]
	p.capabilityMu.Unlock()
	if ok && now.Before(entry.expiresAt) {
		return append(tonemap.Capabilities(nil), entry.capabilities...), nil
	}
	cfg := p.config()
	if cfg == nil || strings.TrimSpace(cfg.Auth.JWTSecret) == "" {
		p.cacheToneMapCapabilityFailure(nodeURL)
		return nil, errors.New("transcode node credentials unavailable")
	}
	requestCtx, cancel := context.WithTimeout(ctx, remoteToneMapCapabilityTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, nodeURL+"/hw-capabilities", nil)
	if err != nil {
		p.cacheToneMapCapabilityFailure(nodeURL)
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+cfg.Auth.JWTSecret)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		p.cacheToneMapCapabilityFailure(nodeURL)
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		p.cacheToneMapCapabilityFailure(nodeURL)
		return nil, fmt.Errorf("transcode node returned %d", response.StatusCode)
	}
	var info playback.HWAccelInfo
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		p.cacheToneMapCapabilityFailure(nodeURL)
		return nil, err
	}
	entry = remoteToneMapCapabilities{capabilities: append(tonemap.Capabilities(nil), info.ToneMapCapabilities...), expiresAt: time.Now().Add(remoteToneMapCapabilityTTL)}
	p.capabilityMu.Lock()
	if p.capabilities == nil {
		p.capabilities = make(map[string]remoteToneMapCapabilities)
	}
	p.capabilities[nodeURL] = entry
	p.capabilityMu.Unlock()
	return append(tonemap.Capabilities(nil), entry.capabilities...), nil
}

// cacheToneMapCapabilityFailure negatively caches an unreachable or invalid
// node briefly so repeated artifact planning does not amplify the failure.
func (p *NodeAwarePreparer) cacheToneMapCapabilityFailure(nodeURL string) {
	p.capabilityMu.Lock()
	if p.capabilities == nil {
		p.capabilities = make(map[string]remoteToneMapCapabilities)
	}
	p.capabilities[nodeURL] = remoteToneMapCapabilities{capabilities: tonemap.Capabilities{}, expiresAt: time.Now().Add(remoteToneMapCapabilityErrorTTL)}
	p.capabilityMu.Unlock()
}

func remotePreparedArtifact(node *nodepool.Node, result downloadprepare.Result) PreparedArtifact {
	group := ""
	if node.Group != nil {
		group = *node.Group
	}
	return PreparedArtifact{
		OriginNodeID:     node.ID,
		OriginNodeURL:    strings.TrimRight(node.URL, "/"),
		OriginNodeGroup:  group,
		OriginArtifactID: result.ArtifactID,
		FileSize:         result.FileSize,
	}
}

func (p *NodeAwarePreparer) ResolveArtifact(ctx context.Context, artifact *Artifact) error {
	if artifact == nil || artifact.OriginNodeID == 0 || p.planner == nil {
		return ErrArtifactOriginRemoved
	}
	node, ok := p.planner.TranscodeNode(artifact.OriginNodeID)
	if !ok || node == nil {
		if p.originLookup != nil {
			configured, err := p.originLookup.GetByID(ctx, artifact.OriginNodeID)
			switch {
			case err == nil && configured != nil && configured.Type == nodepool.NodeTypeTranscode:
				applyArtifactOrigin(artifact, configured)
				if configured.Enabled {
					return nil
				}
			case err != nil && !errors.Is(err, nodepool.ErrNodeNotFound):
				return fmt.Errorf("looking up artifact origin node: %w", err)
			}
		}
		return ErrArtifactOriginRemoved
	}
	applyArtifactOrigin(artifact, node)
	return nil
}

func applyArtifactOrigin(artifact *Artifact, node *nodepool.Node) {
	artifact.OriginNodeURL = strings.TrimRight(node.URL, "/")
	artifact.OriginNodeGroup = ""
	if node.Group != nil {
		artifact.OriginNodeGroup = *node.Group
	}
}

func (p *NodeAwarePreparer) StatArtifact(ctx context.Context, artifact *Artifact) (downloadprepare.Result, error) {
	if err := p.ResolveArtifact(ctx, artifact); err != nil {
		return downloadprepare.Result{}, err
	}
	secret, err := p.remoteCredentials(artifact)
	if err != nil {
		return downloadprepare.Result{}, err
	}
	return p.remote.Stat(ctx, artifact.OriginNodeURL, secret, artifact.OriginArtifactID)
}

func (p *NodeAwarePreparer) DeleteArtifact(ctx context.Context, artifact *Artifact) error {
	// Prefer the authoritative current URL, including for a disabled node. A
	// deleted node has no newer record, so retain the last persisted URL as the
	// best-effort cleanup target.
	_ = p.ResolveArtifact(ctx, artifact)
	secret, err := p.remoteCredentials(artifact)
	if err != nil {
		return err
	}
	return p.remote.Delete(ctx, artifact.OriginNodeURL, secret, artifact.OriginArtifactID)
}

func (p *NodeAwarePreparer) remoteCredentials(artifact *Artifact) (string, error) {
	if artifact == nil || strings.TrimSpace(artifact.OriginNodeURL) == "" || !downloadprepare.ValidArtifactID(artifact.OriginArtifactID) {
		return "", errors.New("remote artifact locator is incomplete")
	}
	cfg := p.config()
	if cfg == nil || strings.TrimSpace(cfg.Auth.JWTSecret) == "" || p.remote == nil {
		return "", errors.New("remote artifact credentials are unavailable")
	}
	return strings.TrimSpace(cfg.Auth.JWTSecret), nil
}

func (p *NodeAwarePreparer) config() *config.Config {
	if p == nil || p.liveCfg == nil {
		return nil
	}
	return p.liveCfg()
}
