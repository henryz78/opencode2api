package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const protocolCapabilityCacheVersion = 1

type protocolCapabilityCache struct {
	Version     int                          `json:"version"`
	UpdatedAt   time.Time                    `json:"updated_at"`
	Protocols   map[Tier]map[string]Protocol `json:"protocols"`
	Unsupported map[Tier]map[string]bool     `json:"unsupported"`
}

func (g *Gateway) loadProtocolCapabilityCache() error {
	if g == nil || g.capabilityCachePath == "" {
		return nil
	}
	data, err := os.ReadFile(g.capabilityCachePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var cache protocolCapabilityCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return fmt.Errorf("decode cache: %w", err)
	}
	if cache.Version != protocolCapabilityCacheVersion {
		return fmt.Errorf("unsupported cache version %d", cache.Version)
	}
	capabilities := protocolCapabilities{Protocols: cache.Protocols, Unsupported: cache.Unsupported}
	if err := validateProtocolCapabilities(capabilities); err != nil {
		return fmt.Errorf("validate cache: %w", err)
	}
	if len(cache.Protocols) == 0 && len(cache.Unsupported) == 0 {
		return errors.New("cache contains no protocol capability data")
	}
	g.catalog.ReplaceWithCapabilities(nil, nil, cache.Protocols, cache.Unsupported)
	if !cache.UpdatedAt.IsZero() {
		g.logger.Info("loaded OpenCode capability catalog cache", "component", "models", "event", "capability_cache_loaded", "updated_at", cache.UpdatedAt.UTC())
	}
	return nil
}

func (g *Gateway) saveProtocolCapabilityCache(capabilities protocolCapabilities) error {
	if g == nil || g.capabilityCachePath == "" {
		return nil
	}
	if err := validateProtocolCapabilities(capabilities); err != nil {
		return err
	}
	if len(capabilities.Protocols) == 0 && len(capabilities.Unsupported) == 0 {
		return errors.New("capability response contains no data")
	}
	cache := protocolCapabilityCache{
		Version:     protocolCapabilityCacheVersion,
		UpdatedAt:   time.Now().UTC(),
		Protocols:   capabilities.Protocols,
		Unsupported: capabilities.Unsupported,
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	directory := filepath.Dir(g.capabilityCachePath)
	temp, err := os.CreateTemp(directory, ".opencode2api-capabilities-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, g.capabilityCachePath); err != nil {
		// Windows cannot replace an existing file with Rename. Removing only
		// this generated cache keeps the update recoverable through the temp
		// file until the final rename succeeds.
		if removeErr := os.Remove(g.capabilityCachePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		if renameErr := os.Rename(tempName, g.capabilityCachePath); renameErr != nil {
			return renameErr
		}
	}
	return nil
}

func validateProtocolCapabilities(capabilities protocolCapabilities) error {
	for tier, protocols := range capabilities.Protocols {
		if tier != TierZen && tier != TierGo {
			return fmt.Errorf("unknown tier %q", tier)
		}
		for model, protocol := range protocols {
			if model == "" || !validProtocol(protocol) {
				return fmt.Errorf("invalid protocol mapping %q: %q", model, protocol)
			}
		}
	}
	for tier, models := range capabilities.Unsupported {
		if tier != TierZen && tier != TierGo {
			return fmt.Errorf("unknown unsupported tier %q", tier)
		}
		for model, unsupported := range models {
			if model == "" || !unsupported {
				return fmt.Errorf("invalid unsupported model %q", model)
			}
		}
	}
	return nil
}
