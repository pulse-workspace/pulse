// Package extension defines the hook interfaces for extending Pulse Core
// with commercial and enterprise capabilities (SSO, Compliance, MFA, Bridges, SaaS).
package extension

import (
	"context"

	"github.com/go-chi/chi/v5"
	"github.com/pulse-workspace/pulse/pkg/core"
)

// Plugin is the contract for enterprise and third-party extensions.
type Plugin interface {
	// Name returns the unique identifier of the plugin (e.g. "sso", "compliance").
	Name() string
	// Tier returns the minimum tier required (e.g. "scale", "enterprise").
	Tier() string
	// Init initializes the plugin with the cell bundle and mounts API routes.
	Init(ctx context.Context, c *core.Cell, router chi.Router) error
}

var (
	registry = make(map[string]Plugin)
)

// Register registers an extension plugin into the runtime.
func Register(p Plugin) {
	if p != nil {
		registry[p.Name()] = p
	}
}

// Plugins returns all registered plugins.
func Plugins() map[string]Plugin {
	return registry
}

