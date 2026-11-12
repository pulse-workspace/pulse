package core

import (
	"context"
	"errors"

	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/config"
	"github.com/pulse-workspace/pulse/pkg/shared/db"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
	"github.com/pulse-workspace/pulse/pkg/shared/nats"
)

// Cell bundles all dependencies a domain service or enterprise plugin needs.
type Cell struct {
	Cfg    *config.Config
	Log    logger.Logger
	DB     *db.DB
	NATS   *nats.Client
	Issuer *authn.Issuer
}

// New creates a Cell with the given dependencies.
func New(cfg *config.Config, log logger.Logger, d *db.DB, n *nats.Client, issuer *authn.Issuer) *Cell {
	return &Cell{
		Cfg:    cfg,
		Log:    log.With().Str("component", "cell").Logger(),
		DB:     d,
		NATS:   n,
		Issuer: issuer,
	}
}

// Service is the interface every domain service or enterprise plugin implements.
type Service interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// ComplianceScope identifies the domain object target of a legal hold.
type ComplianceScope string

const (
	ScopeConv ComplianceScope = "conv"
	ScopeUser ComplianceScope = "user"
)

// ErrNotLicensed indicates an enterprise feature is not licensed or disabled.
var ErrNotLicensed = errors.New("enterprise module: not licensed")

