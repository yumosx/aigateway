package router

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"path/filepath"
	"time"

	"github.com/aegisflow/aegisflow/internal/config"
	"github.com/aegisflow/aegisflow/internal/provider"
	"github.com/aegisflow/aegisflow/internal/rollout"
	"github.com/aegisflow/aegisflow/pkg/types"
)

type Route struct {
	Pattern    string
	Providers  []string
	Strategy   Strategy
}

type RoutedResult struct {
	Response *types.ChatCompletionResponse
	Provider string
}

type Router struct {
	routes         []Route
	registry       *provider.Registry
	circuitBreaker *CircuitBreaker
	rolloutMgr     *rollout.Manager
}

func (r *Router) SetRolloutManager(mgr *rollout.Manager) {
	r.rolloutMgr = mgr
}

func NewRouter(cfg []config.RouteConfig, registry *provider.Registry) *Router {
	routes := make([]Route, len(cfg))
	for i, rc := range cfg {
		routes[i] = Route{
			Pattern:   rc.Match.Model,
			Providers: rc.Providers,
			Strategy:  NewStrategy(rc.Strategy),
		}
	}
	return &Router{
		routes:         routes,
		registry:       registry,
		circuitBreaker: NewCircuitBreaker(3, 30*time.Second),
	}
}

func (r *Router) Route(ctx context.Context, req *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error) {
	result, err := r.RouteWithProvider(ctx, req)
	if err != nil {
		return nil, err
	}
	return result.Response, nil
}

func (r *Router) RouteWithProvider(ctx context.Context, req *types.ChatCompletionRequest) (*RoutedResult, error) {
	// Check for active canary rollout.
	if r.rolloutMgr != nil {
		if active := r.rolloutMgr.ActiveRollout(req.Model); active != nil {
			return r.routeWithCanary(ctx, req, active)
		}
	}

	providers, err := r.resolveProviders(req.Model)
	if err != nil {
		return nil, err
	}

	return r.tryProviders(ctx, req, providers)
}

func (r *Router) routeWithCanary(ctx context.Context, req *types.ChatCompletionRequest, active *rollout.Rollout) (*RoutedResult, error) {
	// Decide whether to send to canary based on percentage.
	if rand.Intn(100) < active.CurrentPercentage {
		// Try canary provider first.
		canary, err := r.registry.Get(active.CanaryProvider)
		if err == nil && !r.circuitBreaker.IsOpen(canary.Name()) {
			resp, err := canary.ChatCompletion(ctx, req)
			if err == nil {
				r.circuitBreaker.RecordSuccess(canary.Name())
				return &RoutedResult{Response: resp, Provider: canary.Name()}, nil
			}
			r.circuitBreaker.RecordFailure(canary.Name())
		}
	}

	// Fall back to baseline providers (excluding canary).
	var baselineProviders []provider.Provider
	for _, name := range active.BaselineProviders {
		if name == active.CanaryProvider {
			continue
		}
		p, err := r.registry.Get(name)
		if err != nil {
			continue
		}
		baselineProviders = append(baselineProviders, p)
	}

	if len(baselineProviders) == 0 {
		// Fall back to normal route resolution.
		providers, err := r.resolveProviders(req.Model)
		if err != nil {
			return nil, err
		}
		return r.tryProviders(ctx, req, providers)
	}

	return r.tryProviders(ctx, req, baselineProviders)
}

func (r *Router) tryProviders(ctx context.Context, req *types.ChatCompletionRequest, providers []provider.Provider) (*RoutedResult, error) {
	var lastErr error
	for _, p := range providers {
		if r.circuitBreaker.IsOpen(p.Name()) {
			continue
		}

		resp, err := p.ChatCompletion(ctx, req)
		if err != nil {
			r.circuitBreaker.RecordFailure(p.Name())
			lastErr = err
			continue
		}

		r.circuitBreaker.RecordSuccess(p.Name())
		return &RoutedResult{Response: resp, Provider: p.Name()}, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all providers failed, last error: %w", lastErr)
	}
	return nil, fmt.Errorf("no available providers for model %q", req.Model)
}

func (r *Router) RouteStream(ctx context.Context, req *types.ChatCompletionRequest) (io.ReadCloser, error) {
	providers, err := r.resolveProviders(req.Model)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, p := range providers {
		if r.circuitBreaker.IsOpen(p.Name()) {
			continue
		}

		stream, err := p.ChatCompletionStream(ctx, req)
		if err != nil {
			r.circuitBreaker.RecordFailure(p.Name())
			lastErr = err
			continue
		}

		r.circuitBreaker.RecordSuccess(p.Name())
		return stream, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all providers failed, last error: %w", lastErr)
	}
	return nil, fmt.Errorf("no available providers for model %q", req.Model)
}

func (r *Router) resolveProviders(model string) ([]provider.Provider, error) {
	for _, route := range r.routes {
		matched, _ := filepath.Match(route.Pattern, model)
		if !matched {
			continue
		}

		var providers []provider.Provider
		for _, name := range route.Providers {
			p, err := r.registry.Get(name)
			if err != nil {
				continue
			}
			providers = append(providers, p)
		}

		if len(providers) == 0 {
			continue
		}

		return route.Strategy.Select(providers), nil
	}

	return nil, fmt.Errorf("no route matched for model %q", model)
}
