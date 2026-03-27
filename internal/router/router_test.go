package router

import (
	"context"
	"testing"
	"time"

	"github.com/aegisflow/aegisflow/internal/admin"
	"github.com/aegisflow/aegisflow/internal/config"
	"github.com/aegisflow/aegisflow/internal/provider"
	"github.com/aegisflow/aegisflow/internal/rollout"
	"github.com/aegisflow/aegisflow/pkg/types"
)

func setupTestRouter() *Router {
	registry := provider.NewRegistry()
	registry.Register(provider.NewMockProvider("mock", 0))
	registry.Register(provider.NewMockProvider("backup", 0))

	routes := []config.RouteConfig{
		{
			Match:     config.RouteMatch{Model: "gpt-*"},
			Providers: []string{"mock", "backup"},
			Strategy:  "priority",
		},
		{
			Match:     config.RouteMatch{Model: "*"},
			Providers: []string{"mock"},
			Strategy:  "priority",
		},
	}

	return NewRouter(routes, registry)
}

func TestRouteExactMatch(t *testing.T) {
	r := setupTestRouter()

	req := &types.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []types.Message{{Role: "user", Content: "Hello"}},
	}

	resp, err := r.Route(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if len(resp.Choices) != 1 {
		t.Errorf("expected 1 choice, got %d", len(resp.Choices))
	}
}

func TestRouteWildcard(t *testing.T) {
	r := setupTestRouter()

	req := &types.ChatCompletionRequest{
		Model:    "some-random-model",
		Messages: []types.Message{{Role: "user", Content: "Hello"}},
	}

	resp, err := r.Route(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestRouteStream(t *testing.T) {
	r := setupTestRouter()

	req := &types.ChatCompletionRequest{
		Model:    "gpt-4o",
		Stream:   true,
		Messages: []types.Message{{Role: "user", Content: "Hello"}},
	}

	stream, err := r.RouteStream(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer stream.Close()
}

func TestCircuitBreakerOpens(t *testing.T) {
	cb := NewCircuitBreaker(2, 1*time.Second)

	cb.RecordFailure("test-provider")
	if cb.IsOpen("test-provider") {
		t.Error("circuit should not be open after 1 failure (threshold=2)")
	}

	cb.RecordFailure("test-provider")
	if !cb.IsOpen("test-provider") {
		t.Error("circuit should be open after 2 failures (threshold=2)")
	}
}

func TestCircuitBreakerResets(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond)

	cb.RecordFailure("test-provider")
	if !cb.IsOpen("test-provider") {
		t.Error("circuit should be open")
	}

	time.Sleep(60 * time.Millisecond)
	if cb.IsOpen("test-provider") {
		t.Error("circuit should have reset after timeout")
	}
}

func TestCircuitBreakerSuccessResets(t *testing.T) {
	cb := NewCircuitBreaker(1, 30*time.Second)

	cb.RecordFailure("test-provider")
	if !cb.IsOpen("test-provider") {
		t.Error("circuit should be open")
	}

	cb.RecordSuccess("test-provider")
	if cb.IsOpen("test-provider") {
		t.Error("circuit should be closed after success")
	}
}

func TestRoundRobinStrategy(t *testing.T) {
	registry := provider.NewRegistry()
	p1 := provider.NewMockProvider("provider-a", 0)
	p2 := provider.NewMockProvider("provider-b", 0)
	registry.Register(p1)
	registry.Register(p2)

	strategy := &RoundRobinStrategy{}
	providers := []provider.Provider{p1, p2}

	first := strategy.Select(providers)
	second := strategy.Select(providers)

	if first[0].Name() == second[0].Name() {
		t.Error("round-robin should rotate the first provider")
	}
}

func TestCanaryRouting(t *testing.T) {
	registry := provider.NewRegistry()
	registry.Register(provider.NewMockProvider("openai", 0))
	registry.Register(provider.NewMockProvider("azure", 0))

	routes := []config.RouteConfig{
		{
			Match:     config.RouteMatch{Model: "gpt-*"},
			Providers: []string{"openai", "azure"},
			Strategy:  "priority",
		},
	}

	router := NewRouter(routes, registry)

	store := rollout.NewMemoryStore()
	reqLog := admin.NewRequestLog(100)
	mgr, err := rollout.NewManager(store, reqLog)
	if err != nil {
		t.Fatalf("failed to create rollout manager: %v", err)
	}

	_, err = mgr.CreateRollout(
		"gpt-4o",
		[]string{"openai"},
		"azure",
		[]int{50},
		5*time.Minute,
		0.05,
		500,
	)
	if err != nil {
		t.Fatalf("failed to create rollout: %v", err)
	}

	router.SetRolloutManager(mgr)

	canaryCount := 0
	baselineCount := 0
	total := 100

	for i := 0; i < total; i++ {
		req := &types.ChatCompletionRequest{
			Model:    "gpt-4o",
			Messages: []types.Message{{Role: "user", Content: "Hello"}},
		}

		result, err := router.RouteWithProvider(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error on iteration %d: %v", i, err)
		}

		switch result.Provider {
		case "azure":
			canaryCount++
		case "openai":
			baselineCount++
		default:
			t.Fatalf("unexpected provider: %s", result.Provider)
		}
	}

	t.Logf("canary=%d baseline=%d", canaryCount, baselineCount)

	if canaryCount < 30 || canaryCount > 70 {
		t.Errorf("expected canary count between 30-70, got %d", canaryCount)
	}
}

func TestRegionRouting(t *testing.T) {
	registry := provider.NewRegistry()
	registry.Register(provider.NewMockProvider("providerA", 0))
	registry.Register(provider.NewMockProvider("providerB", 0))

	routes := []config.RouteConfig{
		{
			Match:    config.RouteMatch{Model: "gpt-*"},
			Strategy: "priority",
			Regions: []config.RegionConfig{
				{Name: "us", Providers: []string{"providerA"}, Strategy: "priority"},
				{Name: "eu", Providers: []string{"providerB"}, Strategy: "priority"},
			},
		},
	}

	router := NewRouter(routes, registry)

	req := &types.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []types.Message{{Role: "user", Content: "Hello"}},
	}

	result, err := router.RouteWithProvider(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Provider != "providerA" {
		t.Errorf("expected provider 'providerA', got %q", result.Provider)
	}
	if result.Region != "us" {
		t.Errorf("expected region 'us', got %q", result.Region)
	}
}

func TestRegionFailover(t *testing.T) {
	registry := provider.NewRegistry()
	registry.Register(provider.NewMockProvider("providerA", 0))
	registry.Register(provider.NewMockProvider("providerB", 0))

	routes := []config.RouteConfig{
		{
			Match:    config.RouteMatch{Model: "gpt-*"},
			Strategy: "priority",
			Regions: []config.RegionConfig{
				{Name: "us", Providers: []string{"providerA"}, Strategy: "priority"},
				{Name: "eu", Providers: []string{"providerB"}, Strategy: "priority"},
			},
		},
	}

	router := NewRouter(routes, registry)

	// Circuit-break all providers in the "us" region.
	router.circuitBreaker.RecordFailure("providerA")
	router.circuitBreaker.RecordFailure("providerA")
	router.circuitBreaker.RecordFailure("providerA")

	req := &types.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []types.Message{{Role: "user", Content: "Hello"}},
	}

	result, err := router.RouteWithProvider(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Provider != "providerB" {
		t.Errorf("expected failover to 'providerB', got %q", result.Provider)
	}
	if result.Region != "eu" {
		t.Errorf("expected failover to region 'eu', got %q", result.Region)
	}
}

func TestRegionBackwardCompat(t *testing.T) {
	registry := provider.NewRegistry()
	registry.Register(provider.NewMockProvider("openai", 0))
	registry.Register(provider.NewMockProvider("azure", 0))

	routes := []config.RouteConfig{
		{
			Match:     config.RouteMatch{Model: "gpt-*"},
			Providers: []string{"openai", "azure"},
			Strategy:  "priority",
		},
	}

	router := NewRouter(routes, registry)

	req := &types.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []types.Message{{Role: "user", Content: "Hello"}},
	}

	result, err := router.RouteWithProvider(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", result.Provider)
	}
	if result.Region != "" {
		t.Errorf("expected empty region for non-region route, got %q", result.Region)
	}
}

func TestNoCanaryWithoutRollout(t *testing.T) {
	registry := provider.NewRegistry()
	registry.Register(provider.NewMockProvider("openai", 0))
	registry.Register(provider.NewMockProvider("azure", 0))

	routes := []config.RouteConfig{
		{
			Match:     config.RouteMatch{Model: "gpt-*"},
			Providers: []string{"openai", "azure"},
			Strategy:  "priority",
		},
	}

	router := NewRouter(routes, registry)

	// Do NOT set a rollout manager — normal routing should occur.
	req := &types.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []types.Message{{Role: "user", Content: "Hello"}},
	}

	result, err := router.RouteWithProvider(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With priority strategy and no rollout, first provider (openai) should be used.
	if result.Provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", result.Provider)
	}

	if result.Response == nil {
		t.Fatal("expected non-nil response")
	}
}
