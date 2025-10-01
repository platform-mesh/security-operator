package controller

import (
	"context"

	"github.com/platform-mesh/golang-commons/logger"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

// providerRunnable wraps the apiexport provider to make it compatible with controller-runtime manager
type providerRunnable struct {
	provider provider
	mcMgr    mcmanager.Manager
	log      *logger.Logger
}

// provider interface abstact kcp related providers
type provider interface {
	Run(ctx context.Context, mgr mcmanager.Manager) error
}

func (p *providerRunnable) Start(ctx context.Context) error {
	err := p.provider.Run(ctx, p.mcMgr)

	// Check if context was cancelled during provider run
	if ctx.Err() != nil {
		p.log.Info().Msg("Context cancelled during provider run")
		return ctx.Err()
	}

	// If provider returned without context cancellation, it's an error
	if err != nil {
		p.log.Error().
			Err(err).Msg("KCP provider failed, retrying with backoff")
	} else {
		p.log.Warn().Msg("KCP provider stopped unexpectedly, retrying with backoff")
	}
	return err
}
