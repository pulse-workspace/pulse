// Package cell hosts the per-cell domain services.
//
// A cell is a self-contained Pulse node that owns a subset of users/groups.
// Cells communicate with each other via NATS JetStream for cross-cell events.
package cell

import (
	"github.com/pulse-im/pulse/pkg/core"
	"github.com/pulse-im/pulse/pkg/shared/authn"
	"github.com/pulse-im/pulse/pkg/shared/config"
	"github.com/pulse-im/pulse/pkg/shared/db"
	"github.com/pulse-im/pulse/pkg/shared/logger"
	"github.com/pulse-im/pulse/pkg/shared/nats"
)

// Cell bundles all dependencies a per-cell service needs.
type Cell = core.Cell

// Service is the interface every domain service implements.
type Service = core.Service

// New creates a Cell with the given dependencies.
func New(cfg *config.Config, log logger.Logger, d *db.DB, n *nats.Client, issuer *authn.Issuer) *Cell {
	return core.New(cfg, log, d, n, issuer)
}
