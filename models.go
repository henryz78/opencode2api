package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	modelMetadataURL             = "https://models.dev/api.json"
	modelMetadataRefreshInterval = 24 * time.Hour
	modelMetadataRequestTimeout  = 30 * time.Second
)

type modelsDevResponse map[string]modelsDevProvider

type modelsDevProvider struct {
	Models map[string]modelsDevModel `json:"models"`
}

type modelsDevModel struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Status string         `json:"status"`
	Cost   *modelsDevCost `json:"cost"`
}

type modelsDevCost struct {
	Input  *float64 `json:"input"`
	Output *float64 `json:"output"`
}

type modelMetadataEntry struct {
	ID         string  `json:"id"`
	Name       string  `json:"name,omitempty"`
	Status     string  `json:"status,omitempty"`
	KnownCost  bool    `json:"known_cost"`
	Free       bool    `json:"free"`
	CostInput  float64 `json:"cost_input,omitempty"`
	CostOutput float64 `json:"cost_output,omitempty"`
}

type modelMetadataCache struct {
	UpdatedAt time.Time                     `json:"updated_at"`
	Models    map[string]modelMetadataEntry `json:"models"`
}

type modelMetadataSnapshot struct {
	Ready     bool      `json:"ready"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Stale     bool      `json:"stale"`
	Models    int       `json:"models"`
	LastError string    `json:"last_error,omitempty"`
}

type modelMetadataCatalog struct {
	mu        sync.RWMutex
	cachePath string
	models    map[string]modelMetadataEntry
	updatedAt time.Time
	lastError string
}

func newModelMetadataCatalog(cachePath string) *modelMetadataCatalog {
	catalog := &modelMetadataCatalog{cachePath: cachePath, models: make(map[string]modelMetadataEntry)}
	if cachePath == "" {
		return catalog
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return catalog
	}
	var cache modelMetadataCache
	if err := json.Unmarshal(data, &cache); err != nil || cache.Models == nil {
		return catalog
	}
	catalog.models = cache.Models
	catalog.updatedAt = cache.UpdatedAt
	return catalog
}

func (c *modelMetadataCatalog) Start(ctx context.Context, logger *slog.Logger) {
	go func() {
		refresh := func() {
			if err := c.refresh(ctx); err != nil {
				c.mu.Lock()
				c.lastError = err.Error()
				c.mu.Unlock()
				logger.Warn("model metadata refresh failed", "component", "models", "event", "metadata_refresh_failed", "error", err)
				return
			}
			logger.Info("model metadata refreshed", "component", "models", "event", "metadata_refreshed", "models", c.count())
		}
		if c.isStale() {
			refresh()
		}
		ticker := time.NewTicker(modelMetadataRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refresh()
			}
		}
	}()
}

func (c *modelMetadataCatalog) refresh(ctx context.Context) error {
	requestCtx, cancel := context.WithTimeout(ctx, modelMetadataRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, modelMetadataURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: modelMetadataRequestTimeout}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("models.dev returned HTTP %d", response.StatusCode)
	}
	var remote modelsDevResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<20))
	if err := decoder.Decode(&remote); err != nil {
		return err
	}
	provider, ok := remote["opencode"]
	if !ok || len(provider.Models) == 0 {
		return errors.New("models.dev response has no opencode models")
	}
	next := make(map[string]modelMetadataEntry, len(provider.Models))
	for key, model := range provider.Models {
		id := model.ID
		if id == "" {
			id = key
		}
		normalized := normalizeModelID(id)
		if normalized == "" {
			continue
		}
		entry := modelMetadataEntry{ID: id, Name: model.Name, Status: model.Status}
		if model.Cost != nil && model.Cost.Input != nil && model.Cost.Output != nil {
			entry.KnownCost = true
			entry.CostInput = *model.Cost.Input
			entry.CostOutput = *model.Cost.Output
			entry.Free = entry.Status != "deprecated" && entry.CostInput == 0 && entry.CostOutput == 0
		}
		next[normalized] = entry
	}
	if len(next) == 0 {
		return errors.New("models.dev response has no usable opencode model entries")
	}
	updatedAt := time.Now().UTC()
	cache := modelMetadataCache{UpdatedAt: updatedAt, Models: next}
	c.mu.Lock()
	c.models = next
	c.updatedAt = updatedAt
	c.lastError = ""
	c.mu.Unlock()
	if c.cachePath != "" {
		data, err := json.MarshalIndent(cache, "", "  ")
		if err != nil {
			return fmt.Errorf("encode model metadata cache: %w", err)
		}
		if err := os.WriteFile(c.cachePath, data, 0600); err != nil {
			c.mu.Lock()
			c.lastError = err.Error()
			c.mu.Unlock()
			return fmt.Errorf("write model metadata cache: %w", err)
		}
	}
	return nil
}

func (c *modelMetadataCatalog) Lookup(model string) (modelMetadataEntry, bool) {
	c.mu.RLock()
	entry, ok := c.models[normalizeModelID(model)]
	c.mu.RUnlock()
	return entry, ok
}

func (c *modelMetadataCatalog) IsFree(model string) bool {
	if entry, ok := c.Lookup(model); ok {
		return entry.KnownCost && entry.Free
	}
	return false
}

func (c *modelMetadataCatalog) Snapshot() modelMetadataSnapshot {
	c.mu.RLock()
	updatedAt, count, lastError := c.updatedAt, len(c.models), c.lastError
	c.mu.RUnlock()
	return modelMetadataSnapshot{
		Ready:     !updatedAt.IsZero(),
		UpdatedAt: updatedAt,
		Stale:     updatedAt.IsZero() || time.Since(updatedAt) >= modelMetadataRefreshInterval,
		Models:    count,
		LastError: lastError,
	}
}

func (c *modelMetadataCatalog) isStale() bool {
	c.mu.RLock()
	updatedAt := c.updatedAt
	c.mu.RUnlock()
	return updatedAt.IsZero() || time.Since(updatedAt) >= modelMetadataRefreshInterval
}

func (c *modelMetadataCatalog) count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.models)
}

func normalizeModelID(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if slash := strings.LastIndex(normalized, "/"); slash >= 0 {
		normalized = normalized[slash+1:]
	}
	return normalized
}

type Protocol string

const (
	ProtocolChat      Protocol = "chat"
	ProtocolResponses Protocol = "responses"
	ProtocolAnthropic Protocol = "anthropic"
)

func validProtocol(p Protocol) bool {
	return p == ProtocolChat || p == ProtocolResponses || p == ProtocolAnthropic
}

type Tier string

const (
	TierZen Tier = "zen"
	TierGo  Tier = "go"
)

type modelRoute struct {
	ID        string
	Tier      Tier
	Protocol  Protocol
	Anonymous bool
}

type modelCatalog struct {
	mu        sync.RWMutex
	zen       map[string]bool
	goModels  map[string]bool
	protocols map[string]Protocol
	metadata  *modelMetadataCatalog
	updatedAt time.Time
	prefer    Tier
}

type modelCatalogSnapshot struct {
	Zen       int       `json:"zen"`
	Go        int       `json:"go"`
	Total     int       `json:"total"`
	Exposed   int       `json:"exposed"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

func newModelCatalog(prefer Tier, overrides map[string]string) *modelCatalog {
	protocols := make(map[string]Protocol, len(overrides))
	for model, protocol := range overrides {
		protocols[model] = Protocol(protocol)
	}
	return &modelCatalog{zen: map[string]bool{}, goModels: map[string]bool{}, protocols: protocols, prefer: prefer}
}

func (c *modelCatalog) Replace(zen, goModels []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if zen != nil {
		c.zen = toSet(zen)
	}
	if goModels != nil {
		c.goModels = toSet(goModels)
	}
	c.updatedAt = time.Now()
}

func (c *modelCatalog) CopyState(source *modelCatalog) {
	if source == nil {
		return
	}
	source.mu.RLock()
	zen := make(map[string]bool, len(source.zen))
	goModels := make(map[string]bool, len(source.goModels))
	for model, available := range source.zen {
		zen[model] = available
	}
	for model, available := range source.goModels {
		goModels[model] = available
	}
	updatedAt := source.updatedAt
	source.mu.RUnlock()
	c.mu.Lock()
	c.zen, c.goModels, c.updatedAt = zen, goModels, updatedAt
	c.mu.Unlock()
}

func (c *modelCatalog) Route(model string, hasZenKeys, hasGoKeys, hasAnonymous bool) (modelRoute, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	protocol := c.protocols[model]
	if protocol == "" {
		protocol = inferProtocol(model)
	}
	// OpenCode's public credential is a Zen-only lane. Prefer it for free
	// models that are known to Zen, or while the initial catalog is pending.
	if hasAnonymous && c.anonymousEligible(model) && (c.zen[model] || len(c.zen) == 0 && len(c.goModels) == 0) {
		return modelRoute{ID: model, Tier: TierZen, Protocol: protocol, Anonymous: true}, nil
	}
	// When a model exists on both tiers, honor the configured priority.
	preferGo := c.prefer == TierGo
	if preferGo && c.goModels[model] && hasGoKeys {
		return modelRoute{ID: model, Tier: TierGo, Protocol: protocol}, nil
	}
	if c.zen[model] && hasZenKeys {
		return modelRoute{ID: model, Tier: TierZen, Protocol: protocol}, nil
	}
	if !preferGo && c.goModels[model] && hasGoKeys {
		return modelRoute{ID: model, Tier: TierGo, Protocol: protocol}, nil
	}
	// Model discovery can temporarily fail. Honor the configured priority.
	if len(c.zen) == 0 && len(c.goModels) == 0 {
		if preferGo && hasGoKeys {
			return modelRoute{ID: model, Tier: TierGo, Protocol: protocol}, nil
		}
		if hasZenKeys {
			return modelRoute{ID: model, Tier: TierZen, Protocol: protocol}, nil
		}
		if hasGoKeys {
			return modelRoute{ID: model, Tier: TierGo, Protocol: protocol}, nil
		}
	}
	return modelRoute{}, fmt.Errorf("model %q is not available in the configured Zen or Go pools", model)
}

func isFreeModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "free")
}

func (c *modelCatalog) anonymousEligible(model string) bool {
	if c.metadata != nil {
		return c.metadata.IsFree(model)
	}
	return isFreeModel(model)
}

func (c *modelCatalog) List() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	seen := make(map[string]bool, len(c.zen)+len(c.goModels))
	for model := range c.zen {
		seen[model] = true
	}
	for model := range c.goModels {
		seen[model] = true
	}
	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

func (c *modelCatalog) Snapshot() modelCatalogSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	seen := make(map[string]bool, len(c.zen)+len(c.goModels))
	for model := range c.zen {
		seen[model] = true
	}
	for model := range c.goModels {
		seen[model] = true
	}
	exposed := 0
	for model := range seen {
		if supportedModel(model) {
			exposed++
		}
	}
	return modelCatalogSnapshot{
		Zen:       len(c.zen),
		Go:        len(c.goModels),
		Total:     len(seen),
		Exposed:   exposed,
		UpdatedAt: c.updatedAt,
	}
}

func toSet(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item] = true
	}
	return out
}

func inferProtocol(model string) Protocol {
	m := strings.ToLower(model)
	for _, prefix := range []string{"claude-", "qwen"} {
		if strings.HasPrefix(m, prefix) {
			return ProtocolAnthropic
		}
	}
	for _, prefix := range []string{"gpt-", "o1", "o3", "o4", "grok-", "muse-"} {
		if strings.HasPrefix(m, prefix) {
			return ProtocolResponses
		}
	}
	return ProtocolChat
}

func supportedModel(model string) bool {
	m := strings.ToLower(model)
	return !strings.HasPrefix(m, "gemini-")
}

type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func fetchModels(ctx context.Context, client *http.Client, baseURL, key string) ([]string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", opencodeUserAgent())
	req.Header.Set("x-opencode-client", "cli")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, resp.StatusCode, fmt.Errorf("models endpoint returned HTTP %d", resp.StatusCode)
	}
	var payload modelsResponse
	dec := json.NewDecoder(io.LimitReader(resp.Body, 8<<20))
	if err := dec.Decode(&payload); err != nil {
		return nil, resp.StatusCode, err
	}
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if item.ID != "" {
			models = append(models, item.ID)
		}
	}
	if len(models) == 0 {
		return nil, resp.StatusCode, errors.New("models endpoint returned an empty list")
	}
	return models, resp.StatusCode, nil
}
