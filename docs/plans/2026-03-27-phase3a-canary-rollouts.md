# Phase 3A: A/B Testing & Canary Deployments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow operators to gradually shift LLM traffic from one provider to another with automatic health-based promotion or rollback.

**Architecture:** A new `internal/rollout/` package manages rollout lifecycle and health evaluation. The existing `router.Router` checks for active rollouts before provider selection, applying weighted random routing between baseline and canary. State is persisted in PostgreSQL (with in-memory fallback). Admin API endpoints control rollout lifecycle, and a dashboard page shows live rollout progress.

**Tech Stack:** Go, PostgreSQL, chi router, Chart.js (dashboard)

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/rollout/types.go` | Create | Rollout struct, state constants, CanaryConfig struct |
| `internal/rollout/store.go` | Create | PostgreSQL persistence: create/read/update rollouts table |
| `internal/rollout/memory_store.go` | Create | In-memory fallback store implementing same interface |
| `internal/rollout/evaluator.go` | Create | Health evaluation: error rate + p95 latency calculation, promote/rollback decision |
| `internal/rollout/manager.go` | Create | Rollout lifecycle: create, promote, pause, resume, rollback. Background evaluation goroutine |
| `internal/rollout/manager_test.go` | Create | Tests for state transitions, auto-promote, auto-rollback |
| `internal/rollout/evaluator_test.go` | Create | Tests for health evaluation logic |
| `internal/router/router.go` | Modify | Add canary-aware routing in Route() and RouteStream() |
| `internal/router/router_test.go` | Modify | Add tests for weighted canary routing |
| `internal/config/config.go` | Modify | Add CanaryConfig to RouteConfig |
| `internal/admin/admin.go` | Modify | Add rollout API endpoints + wire rollout manager |
| `internal/admin/requestlog.go` | Modify | Add Provider field to RequestEntry |
| `internal/admin/dashboard.html` | Modify | Add Rollouts nav item + page |
| `cmd/aegisflow/main.go` | Modify | Initialize rollout manager, wire into router + admin |

---

### Task 1: Define rollout types and config

**Files:**
- Create: `internal/rollout/types.go`
- Modify: `internal/config/config.go:84-88`

- [ ] **Step 1: Create rollout types**

Create `internal/rollout/types.go`:

```go
package rollout

import (
	"time"
)

const (
	StatePending    = "pending"
	StateRunning    = "running"
	StatePaused     = "paused"
	StateRolledBack = "rolled_back"
	StateCompleted  = "completed"
)

type Rollout struct {
	ID                  string    `json:"id"`
	RouteModel          string    `json:"route_model"`
	BaselineProviders   []string  `json:"baseline_providers"`
	CanaryProvider      string    `json:"canary_provider"`
	Stages              []int     `json:"stages"`
	CurrentStage        int       `json:"current_stage"`
	CurrentPercentage   int       `json:"current_percentage"`
	State               string    `json:"state"`
	ObservationWindow   time.Duration `json:"observation_window"`
	ErrorThreshold      float64   `json:"error_threshold"`
	LatencyP95Threshold int64     `json:"latency_p95_threshold"`
	StageStartedAt      time.Time `json:"stage_started_at"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
	RollbackReason      string    `json:"rollback_reason,omitempty"`
}

type CanaryConfig struct {
	TargetProvider      string        `yaml:"target_provider"`
	Stages              []int         `yaml:"stages"`
	ObservationWindow   time.Duration `yaml:"observation_window"`
	ErrorThreshold      float64       `yaml:"error_threshold"`
	LatencyP95Threshold int64         `yaml:"latency_p95_threshold"`
}

type HealthMetrics struct {
	ErrorRate    float64 `json:"error_rate"`
	P95LatencyMs int64  `json:"p95_latency_ms"`
	Requests     int64  `json:"requests"`
}

type RolloutMetrics struct {
	Baseline HealthMetrics `json:"baseline"`
	Canary   HealthMetrics `json:"canary"`
}

// Store is the interface for rollout persistence.
type Store interface {
	Create(r *Rollout) error
	Get(id string) (*Rollout, error)
	GetByModel(model string) (*Rollout, error)
	Update(r *Rollout) error
	List() ([]*Rollout, error)
	Migrate() error
}
```

- [ ] **Step 2: Add CanaryConfig to RouteConfig**

In `internal/config/config.go`, replace the `RouteConfig` struct:

```go
type RouteConfig struct {
	Match     RouteMatch     `yaml:"match"`
	Providers []string       `yaml:"providers"`
	Strategy  string         `yaml:"strategy"`
	Canary    *CanaryConfig  `yaml:"canary,omitempty"`
}

type CanaryConfig struct {
	TargetProvider      string        `yaml:"target_provider"`
	Stages              []int         `yaml:"stages"`
	ObservationWindow   time.Duration `yaml:"observation_window"`
	ErrorThreshold      float64       `yaml:"error_threshold"`
	LatencyP95Threshold int64         `yaml:"latency_p95_threshold"`
}
```

Note: import the `CanaryConfig` type is defined in config, not rollout, to avoid circular imports. The rollout package references config.CanaryConfig.

Actually, to keep things clean, define CanaryConfig in config.go only:

```go
type CanaryConfig struct {
	TargetProvider      string        `yaml:"target_provider"`
	Stages              []int         `yaml:"stages"`
	ObservationWindow   time.Duration `yaml:"observation_window"`
	ErrorThreshold      float64       `yaml:"error_threshold"`
	LatencyP95Threshold int64         `yaml:"latency_p95_threshold"`
}
```

And in `internal/rollout/types.go`, remove the duplicate CanaryConfig and import from config where needed, or keep rollout types self-contained by not importing config at all. The Rollout struct already has all fields flattened — it doesn't reference CanaryConfig. So keep both definitions: config.CanaryConfig for YAML parsing, rollout.Rollout for runtime state. No circular import.

- [ ] **Step 3: Build to verify**

```bash
go build ./...
```

Expected: Success.

- [ ] **Step 4: Run existing tests**

```bash
go test ./... -count=1
```

Expected: All existing tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/rollout/types.go internal/config/config.go
git commit -m "Define rollout types, state constants, and canary config"
```

---

### Task 2: Implement rollout stores (PostgreSQL + in-memory)

**Files:**
- Create: `internal/rollout/store.go`
- Create: `internal/rollout/memory_store.go`

- [ ] **Step 1: Create PostgreSQL store**

Create `internal/rollout/store.go`:

```go
package rollout

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) Migrate() error {
	query := `
	CREATE TABLE IF NOT EXISTS rollouts (
		id VARCHAR(36) PRIMARY KEY,
		route_model VARCHAR(255) NOT NULL,
		baseline_providers TEXT NOT NULL,
		canary_provider VARCHAR(255) NOT NULL,
		stages TEXT NOT NULL,
		current_stage INT NOT NULL DEFAULT 0,
		current_percentage INT NOT NULL DEFAULT 0,
		state VARCHAR(20) NOT NULL DEFAULT 'pending',
		observation_window BIGINT NOT NULL,
		error_threshold DOUBLE PRECISION NOT NULL,
		latency_p95_threshold BIGINT NOT NULL,
		stage_started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		completed_at TIMESTAMPTZ,
		rollback_reason TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_rollouts_state ON rollouts(state);
	CREATE INDEX IF NOT EXISTS idx_rollouts_model ON rollouts(route_model);
	`
	_, err := s.db.Exec(query)
	return err
}

func (s *PostgresStore) Create(r *Rollout) error {
	stagesJSON, _ := json.Marshal(r.Stages)
	providersStr := strings.Join(r.BaselineProviders, ",")
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO rollouts (id, route_model, baseline_providers, canary_provider, stages, current_stage, current_percentage, state, observation_window, error_threshold, latency_p95_threshold, stage_started_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		r.ID, r.RouteModel, providersStr, r.CanaryProvider,
		string(stagesJSON), r.CurrentStage, r.CurrentPercentage, r.State,
		r.ObservationWindow.Milliseconds(), r.ErrorThreshold, r.LatencyP95Threshold,
		r.StageStartedAt, r.CreatedAt, r.UpdatedAt,
	)
	return err
}

func (s *PostgresStore) Get(id string) (*Rollout, error) {
	return s.scanRow(s.db.QueryRowContext(context.Background(),
		`SELECT id, route_model, baseline_providers, canary_provider, stages, current_stage, current_percentage, state, observation_window, error_threshold, latency_p95_threshold, stage_started_at, created_at, updated_at, completed_at, rollback_reason
		FROM rollouts WHERE id = $1`, id))
}

func (s *PostgresStore) GetByModel(model string) (*Rollout, error) {
	return s.scanRow(s.db.QueryRowContext(context.Background(),
		`SELECT id, route_model, baseline_providers, canary_provider, stages, current_stage, current_percentage, state, observation_window, error_threshold, latency_p95_threshold, stage_started_at, created_at, updated_at, completed_at, rollback_reason
		FROM rollouts WHERE route_model = $1 AND state IN ('pending','running','paused') ORDER BY created_at DESC LIMIT 1`, model))
}

func (s *PostgresStore) scanRow(row *sql.Row) (*Rollout, error) {
	var r Rollout
	var providersStr, stagesJSON string
	var obsWindowMs int64
	var completedAt sql.NullTime
	err := row.Scan(&r.ID, &r.RouteModel, &providersStr, &r.CanaryProvider,
		&stagesJSON, &r.CurrentStage, &r.CurrentPercentage, &r.State,
		&obsWindowMs, &r.ErrorThreshold, &r.LatencyP95Threshold,
		&r.StageStartedAt, &r.CreatedAt, &r.UpdatedAt, &completedAt, &r.RollbackReason)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	r.BaselineProviders = strings.Split(providersStr, ",")
	json.Unmarshal([]byte(stagesJSON), &r.Stages)
	r.ObservationWindow = time.Duration(obsWindowMs) * time.Millisecond
	if completedAt.Valid {
		r.CompletedAt = &completedAt.Time
	}
	return &r, nil
}

func (s *PostgresStore) Update(r *Rollout) error {
	stagesJSON, _ := json.Marshal(r.Stages)
	r.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(context.Background(),
		`UPDATE rollouts SET current_stage=$1, current_percentage=$2, state=$3, stage_started_at=$4, updated_at=$5, completed_at=$6, rollback_reason=$7, stages=$8
		WHERE id=$9`,
		r.CurrentStage, r.CurrentPercentage, r.State, r.StageStartedAt, r.UpdatedAt, r.CompletedAt, r.RollbackReason, string(stagesJSON), r.ID)
	return err
}

func (s *PostgresStore) List() ([]*Rollout, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT id, route_model, baseline_providers, canary_provider, stages, current_stage, current_percentage, state, observation_window, error_threshold, latency_p95_threshold, stage_started_at, created_at, updated_at, completed_at, rollback_reason
		FROM rollouts ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rollouts []*Rollout
	for rows.Next() {
		var r Rollout
		var providersStr, stagesJSON string
		var obsWindowMs int64
		var completedAt sql.NullTime
		if err := rows.Scan(&r.ID, &r.RouteModel, &providersStr, &r.CanaryProvider,
			&stagesJSON, &r.CurrentStage, &r.CurrentPercentage, &r.State,
			&obsWindowMs, &r.ErrorThreshold, &r.LatencyP95Threshold,
			&r.StageStartedAt, &r.CreatedAt, &r.UpdatedAt, &completedAt, &r.RollbackReason); err != nil {
			return nil, err
		}
		r.BaselineProviders = strings.Split(providersStr, ",")
		json.Unmarshal([]byte(stagesJSON), &r.Stages)
		r.ObservationWindow = time.Duration(obsWindowMs) * time.Millisecond
		if completedAt.Valid {
			r.CompletedAt = &completedAt.Time
		}
		rollouts = append(rollouts, &r)
	}
	return rollouts, rows.Err()
}
```

- [ ] **Step 2: Create in-memory store**

Create `internal/rollout/memory_store.go`:

```go
package rollout

import (
	"fmt"
	"sync"
)

type MemoryStore struct {
	mu       sync.RWMutex
	rollouts map[string]*Rollout
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rollouts: make(map[string]*Rollout)}
}

func (s *MemoryStore) Migrate() error { return nil }

func (s *MemoryStore) Create(r *Rollout) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollouts[r.ID] = r
	return nil
}

func (s *MemoryStore) Get(id string) (*Rollout, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rollouts[id]
	if !ok {
		return nil, nil
	}
	return r, nil
}

func (s *MemoryStore) GetByModel(model string) (*Rollout, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.rollouts {
		if r.RouteModel == model && (r.State == StatePending || r.State == StateRunning || r.State == StatePaused) {
			return r, nil
		}
	}
	return nil, nil
}

func (s *MemoryStore) Update(r *Rollout) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rollouts[r.ID]; !ok {
		return fmt.Errorf("rollout %s not found", r.ID)
	}
	s.rollouts[r.ID] = r
	return nil
}

func (s *MemoryStore) List() ([]*Rollout, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Rollout, 0, len(s.rollouts))
	for _, r := range s.rollouts {
		result = append(result, r)
	}
	return result, nil
}
```

- [ ] **Step 3: Build to verify**

```bash
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add internal/rollout/store.go internal/rollout/memory_store.go
git commit -m "Implement rollout stores: PostgreSQL persistence and in-memory fallback"
```

---

### Task 3: Implement health evaluator

**Files:**
- Create: `internal/rollout/evaluator.go`
- Create: `internal/rollout/evaluator_test.go`

- [ ] **Step 1: Create evaluator**

Create `internal/rollout/evaluator.go`:

```go
package rollout

import (
	"sort"
	"time"

	"github.com/aegisflow/aegisflow/internal/admin"
)

type Evaluator struct {
	requestLog *admin.RequestLog
}

func NewEvaluator(reqLog *admin.RequestLog) *Evaluator {
	return &Evaluator{requestLog: reqLog}
}

const minRequestsForDecision = 10

// Evaluate checks canary health and returns a decision: "promote", "rollback", or "wait".
func (e *Evaluator) Evaluate(r *Rollout) (decision string, reason string, metrics RolloutMetrics) {
	if r.State != StateRunning {
		return "wait", "rollout not running", metrics
	}

	// Check if observation window has elapsed
	if time.Since(r.StageStartedAt) < r.ObservationWindow {
		return "wait", "observation window not elapsed", metrics
	}

	// Get recent requests within the observation window
	entries := e.requestLog.Recent(e.requestLog.Count())
	cutoff := time.Now().Add(-r.ObservationWindow)

	var baselineEntries, canaryEntries []admin.RequestEntry
	for _, entry := range entries {
		if entry.Timestamp.Before(cutoff) {
			continue
		}
		if entry.Model != r.RouteModel {
			continue
		}
		if entry.Provider == r.CanaryProvider {
			canaryEntries = append(canaryEntries, entry)
		} else {
			baselineEntries = append(baselineEntries, entry)
		}
	}

	metrics.Baseline = calculateHealth(baselineEntries)
	metrics.Canary = calculateHealth(canaryEntries)

	// Need minimum requests before making a decision
	if metrics.Canary.Requests < minRequestsForDecision {
		return "wait", "insufficient canary requests", metrics
	}

	// Check thresholds
	if metrics.Canary.ErrorRate > r.ErrorThreshold {
		return "rollback", fmt.Sprintf("canary error rate %.1f%% exceeds threshold %.1f%%", metrics.Canary.ErrorRate, r.ErrorThreshold), metrics
	}
	if metrics.Canary.P95LatencyMs > r.LatencyP95Threshold {
		return "rollback", fmt.Sprintf("canary p95 latency %dms exceeds threshold %dms", metrics.Canary.P95LatencyMs, r.LatencyP95Threshold), metrics
	}

	return "promote", "canary healthy", metrics
}

func calculateHealth(entries []admin.RequestEntry) HealthMetrics {
	if len(entries) == 0 {
		return HealthMetrics{}
	}

	var errors int64
	latencies := make([]int64, 0, len(entries))
	for _, e := range entries {
		if e.Status >= 500 {
			errors++
		}
		latencies = append(latencies, e.LatencyMs)
	}

	total := int64(len(entries))
	errorRate := float64(errors) / float64(total) * 100

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95Idx := int(float64(len(latencies)) * 0.95)
	if p95Idx >= len(latencies) {
		p95Idx = len(latencies) - 1
	}

	return HealthMetrics{
		ErrorRate:    errorRate,
		P95LatencyMs: latencies[p95Idx],
		Requests:     total,
	}
}
```

Add the missing `fmt` import and the `Count()` method to RequestLog. The evaluator needs `RequestLog.Count()` and `RequestEntry.Provider`.

- [ ] **Step 2: Add Provider field and Count method to RequestLog**

In `internal/admin/requestlog.go`, add `Provider` field to `RequestEntry`:

```go
type RequestEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	RequestID  string    `json:"request_id"`
	TenantID   string    `json:"tenant_id"`
	Model      string    `json:"model"`
	Provider   string    `json:"provider,omitempty"`
	Status     int       `json:"status"`
	LatencyMs  int64     `json:"latency_ms"`
	Tokens     int       `json:"tokens"`
	Cached     bool      `json:"cached"`
	PolicyHit  string    `json:"policy_hit,omitempty"`
}
```

Add `Count()` method after the `Add` method:

```go
func (rl *RequestLog) Count() int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.count
}
```

- [ ] **Step 3: Write evaluator tests**

Create `internal/rollout/evaluator_test.go`:

```go
package rollout

import (
	"testing"
	"time"

	"github.com/aegisflow/aegisflow/internal/admin"
)

func newTestRequestLog(entries []admin.RequestEntry) *admin.RequestLog {
	rl := admin.NewRequestLog(500)
	for _, e := range entries {
		rl.Add(e)
	}
	return rl
}

func TestEvaluatePromotesHealthyCanary(t *testing.T) {
	now := time.Now()
	entries := make([]admin.RequestEntry, 0)
	// 20 healthy canary requests
	for i := 0; i < 20; i++ {
		entries = append(entries, admin.RequestEntry{
			Timestamp: now.Add(-time.Duration(i) * time.Second),
			Model:     "gpt-4o",
			Provider:  "azure",
			Status:    200,
			LatencyMs: 400,
		})
	}
	// 80 healthy baseline requests
	for i := 0; i < 80; i++ {
		entries = append(entries, admin.RequestEntry{
			Timestamp: now.Add(-time.Duration(i) * time.Second),
			Model:     "gpt-4o",
			Provider:  "openai",
			Status:    200,
			LatencyMs: 450,
		})
	}

	rl := newTestRequestLog(entries)
	eval := NewEvaluator(rl)

	r := &Rollout{
		RouteModel:          "gpt-4o",
		CanaryProvider:      "azure",
		State:               StateRunning,
		ErrorThreshold:      5.0,
		LatencyP95Threshold: 3000,
		ObservationWindow:   1 * time.Second,
		StageStartedAt:      now.Add(-2 * time.Second),
	}

	decision, _, _ := eval.Evaluate(r)
	if decision != "promote" {
		t.Errorf("expected promote, got %s", decision)
	}
}

func TestEvaluateRollsBackHighErrorRate(t *testing.T) {
	now := time.Now()
	entries := make([]admin.RequestEntry, 0)
	// 12 canary requests, 3 errors (25% error rate)
	for i := 0; i < 12; i++ {
		status := 200
		if i < 3 {
			status = 500
		}
		entries = append(entries, admin.RequestEntry{
			Timestamp: now.Add(-time.Duration(i) * time.Second),
			Model:     "gpt-4o",
			Provider:  "azure",
			Status:    status,
			LatencyMs: 400,
		})
	}

	rl := newTestRequestLog(entries)
	eval := NewEvaluator(rl)

	r := &Rollout{
		RouteModel:          "gpt-4o",
		CanaryProvider:      "azure",
		State:               StateRunning,
		ErrorThreshold:      5.0,
		LatencyP95Threshold: 3000,
		ObservationWindow:   1 * time.Second,
		StageStartedAt:      now.Add(-2 * time.Second),
	}

	decision, _, _ := eval.Evaluate(r)
	if decision != "rollback" {
		t.Errorf("expected rollback, got %s", decision)
	}
}

func TestEvaluateRollsBackHighLatency(t *testing.T) {
	now := time.Now()
	entries := make([]admin.RequestEntry, 0)
	for i := 0; i < 20; i++ {
		entries = append(entries, admin.RequestEntry{
			Timestamp: now.Add(-time.Duration(i) * time.Second),
			Model:     "gpt-4o",
			Provider:  "azure",
			Status:    200,
			LatencyMs: 4000, // above 3000ms threshold
		})
	}

	rl := newTestRequestLog(entries)
	eval := NewEvaluator(rl)

	r := &Rollout{
		RouteModel:          "gpt-4o",
		CanaryProvider:      "azure",
		State:               StateRunning,
		ErrorThreshold:      5.0,
		LatencyP95Threshold: 3000,
		ObservationWindow:   1 * time.Second,
		StageStartedAt:      now.Add(-2 * time.Second),
	}

	decision, _, _ := eval.Evaluate(r)
	if decision != "rollback" {
		t.Errorf("expected rollback, got %s", decision)
	}
}

func TestEvaluateWaitsForMinRequests(t *testing.T) {
	now := time.Now()
	entries := make([]admin.RequestEntry, 0)
	// Only 5 canary requests (below minimum of 10)
	for i := 0; i < 5; i++ {
		entries = append(entries, admin.RequestEntry{
			Timestamp: now.Add(-time.Duration(i) * time.Second),
			Model:     "gpt-4o",
			Provider:  "azure",
			Status:    200,
			LatencyMs: 400,
		})
	}

	rl := newTestRequestLog(entries)
	eval := NewEvaluator(rl)

	r := &Rollout{
		RouteModel:          "gpt-4o",
		CanaryProvider:      "azure",
		State:               StateRunning,
		ErrorThreshold:      5.0,
		LatencyP95Threshold: 3000,
		ObservationWindow:   1 * time.Second,
		StageStartedAt:      now.Add(-2 * time.Second),
	}

	decision, _, _ := eval.Evaluate(r)
	if decision != "wait" {
		t.Errorf("expected wait, got %s", decision)
	}
}

func TestEvaluateWaitsForObservationWindow(t *testing.T) {
	now := time.Now()
	rl := admin.NewRequestLog(100)
	eval := NewEvaluator(rl)

	r := &Rollout{
		RouteModel:          "gpt-4o",
		CanaryProvider:      "azure",
		State:               StateRunning,
		ErrorThreshold:      5.0,
		LatencyP95Threshold: 3000,
		ObservationWindow:   5 * time.Minute,
		StageStartedAt:      now.Add(-1 * time.Minute), // only 1 min elapsed of 5 min window
	}

	decision, _, _ := eval.Evaluate(r)
	if decision != "wait" {
		t.Errorf("expected wait, got %s", decision)
	}
}

func TestCalculateHealthEmpty(t *testing.T) {
	m := calculateHealth(nil)
	if m.Requests != 0 {
		t.Errorf("expected 0 requests, got %d", m.Requests)
	}
}
```

- [ ] **Step 4: Build and run tests**

```bash
go build ./...
go test ./internal/rollout/ -v -count=1
```

Expected: All evaluator tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/rollout/evaluator.go internal/rollout/evaluator_test.go internal/admin/requestlog.go
git commit -m "Implement rollout health evaluator with error rate and p95 latency checks"
```

---

### Task 4: Implement rollout manager

**Files:**
- Create: `internal/rollout/manager.go`
- Create: `internal/rollout/manager_test.go`

- [ ] **Step 1: Create the manager**

Create `internal/rollout/manager.go`:

```go
package rollout

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/aegisflow/aegisflow/internal/admin"
)

type Manager struct {
	store     Store
	evaluator *Evaluator
	mu        sync.RWMutex
	stopCh    chan struct{}
}

func NewManager(store Store, reqLog *admin.RequestLog) (*Manager, error) {
	if err := store.Migrate(); err != nil {
		return nil, fmt.Errorf("migrating rollout store: %w", err)
	}
	return &Manager{
		store:     store,
		evaluator: NewEvaluator(reqLog),
		stopCh:    make(chan struct{}),
	}, nil
}

func (m *Manager) Start() {
	go m.evaluationLoop()
}

func (m *Manager) Stop() {
	close(m.stopCh)
}

func (m *Manager) evaluationLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.evaluateAll()
		}
	}
}

func (m *Manager) evaluateAll() {
	rollouts, err := m.store.List()
	if err != nil {
		log.Printf("rollout manager: failed to list rollouts: %v", err)
		return
	}

	for _, r := range rollouts {
		if r.State != StateRunning {
			continue
		}

		decision, reason, metrics := m.evaluator.Evaluate(r)
		switch decision {
		case "promote":
			m.promote(r, metrics)
		case "rollback":
			m.doRollback(r, reason)
		}
	}
}

func (m *Manager) promote(r *Rollout, metrics RolloutMetrics) {
	nextStage := r.CurrentStage + 1
	if nextStage >= len(r.Stages) {
		// Final stage completed
		now := time.Now()
		r.State = StateCompleted
		r.CompletedAt = &now
		r.UpdatedAt = now
		if err := m.store.Update(r); err != nil {
			log.Printf("rollout %s: failed to complete: %v", r.ID, err)
			return
		}
		log.Printf("rollout %s: completed — canary %s is now primary for %s", r.ID, r.CanaryProvider, r.RouteModel)
		return
	}

	r.CurrentStage = nextStage
	r.CurrentPercentage = r.Stages[nextStage]
	r.StageStartedAt = time.Now()
	r.UpdatedAt = time.Now()
	if err := m.store.Update(r); err != nil {
		log.Printf("rollout %s: failed to promote to stage %d: %v", r.ID, nextStage, err)
		return
	}
	log.Printf("rollout %s: promoted to stage %d (%d%%) for %s", r.ID, nextStage, r.CurrentPercentage, r.RouteModel)
}

func (m *Manager) doRollback(r *Rollout, reason string) {
	r.State = StateRolledBack
	r.CurrentPercentage = 0
	r.RollbackReason = reason
	now := time.Now()
	r.CompletedAt = &now
	r.UpdatedAt = now
	if err := m.store.Update(r); err != nil {
		log.Printf("rollout %s: failed to rollback: %v", r.ID, err)
		return
	}
	log.Printf("rollout %s: rolled back — %s", r.ID, reason)
}

// CreateRollout starts a new canary rollout.
func (m *Manager) CreateRollout(routeModel string, baselineProviders []string, canaryProvider string, stages []int, observationWindow time.Duration, errorThreshold float64, latencyP95Threshold int64) (*Rollout, error) {
	// Check for existing active rollout on this model
	existing, _ := m.store.GetByModel(routeModel)
	if existing != nil {
		return nil, fmt.Errorf("active rollout already exists for model %s (id: %s, state: %s)", routeModel, existing.ID, existing.State)
	}

	now := time.Now()
	r := &Rollout{
		ID:                  fmt.Sprintf("r-%d", now.UnixNano()),
		RouteModel:          routeModel,
		BaselineProviders:   baselineProviders,
		CanaryProvider:      canaryProvider,
		Stages:              stages,
		CurrentStage:        0,
		CurrentPercentage:   stages[0],
		State:               StateRunning,
		ObservationWindow:   observationWindow,
		ErrorThreshold:      errorThreshold,
		LatencyP95Threshold: latencyP95Threshold,
		StageStartedAt:      now,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	if err := m.store.Create(r); err != nil {
		return nil, fmt.Errorf("creating rollout: %w", err)
	}

	log.Printf("rollout %s: created for %s → %s (stages: %v)", r.ID, routeModel, canaryProvider, stages)
	return r, nil
}

// ActiveRollout returns the active rollout for a model, or nil.
func (m *Manager) ActiveRollout(model string) *Rollout {
	r, _ := m.store.GetByModel(model)
	if r != nil && r.State == StateRunning {
		return r
	}
	return nil
}

func (m *Manager) GetRollout(id string) (*Rollout, error) {
	return m.store.Get(id)
}

func (m *Manager) ListRollouts() ([]*Rollout, error) {
	return m.store.List()
}

func (m *Manager) PauseRollout(id string) error {
	r, err := m.store.Get(id)
	if err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("rollout %s not found", id)
	}
	if r.State != StateRunning {
		return fmt.Errorf("cannot pause rollout in state %s", r.State)
	}
	r.State = StatePaused
	r.UpdatedAt = time.Now()
	return m.store.Update(r)
}

func (m *Manager) ResumeRollout(id string) error {
	r, err := m.store.Get(id)
	if err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("rollout %s not found", id)
	}
	if r.State != StatePaused {
		return fmt.Errorf("cannot resume rollout in state %s", r.State)
	}
	r.State = StateRunning
	r.StageStartedAt = time.Now()
	r.UpdatedAt = time.Now()
	return m.store.Update(r)
}

func (m *Manager) RollbackRollout(id string) error {
	r, err := m.store.Get(id)
	if err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("rollout %s not found", id)
	}
	if r.State != StateRunning && r.State != StatePaused {
		return fmt.Errorf("cannot rollback rollout in state %s", r.State)
	}
	m.doRollback(r, "manual rollback")
	return nil
}

// GetMetrics returns current health metrics for a rollout.
func (m *Manager) GetMetrics(r *Rollout) RolloutMetrics {
	_, _, metrics := m.evaluator.Evaluate(r)
	return metrics
}
```

- [ ] **Step 2: Write manager tests**

Create `internal/rollout/manager_test.go`:

```go
package rollout

import (
	"testing"
	"time"

	"github.com/aegisflow/aegisflow/internal/admin"
)

func TestCreateRollout(t *testing.T) {
	store := NewMemoryStore()
	rl := admin.NewRequestLog(100)
	mgr, err := NewManager(store, rl)
	if err != nil {
		t.Fatal(err)
	}

	r, err := mgr.CreateRollout("gpt-4o", []string{"openai"}, "azure", []int{5, 25, 50, 100}, 5*time.Minute, 5.0, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != StateRunning {
		t.Errorf("expected running, got %s", r.State)
	}
	if r.CurrentPercentage != 5 {
		t.Errorf("expected 5%%, got %d%%", r.CurrentPercentage)
	}
}

func TestDuplicateRolloutRejected(t *testing.T) {
	store := NewMemoryStore()
	rl := admin.NewRequestLog(100)
	mgr, _ := NewManager(store, rl)

	_, err := mgr.CreateRollout("gpt-4o", []string{"openai"}, "azure", []int{5, 100}, 5*time.Minute, 5.0, 3000)
	if err != nil {
		t.Fatal(err)
	}

	_, err = mgr.CreateRollout("gpt-4o", []string{"openai"}, "azure2", []int{10, 100}, 5*time.Minute, 5.0, 3000)
	if err == nil {
		t.Error("expected error for duplicate rollout on same model")
	}
}

func TestPauseResumeRollout(t *testing.T) {
	store := NewMemoryStore()
	rl := admin.NewRequestLog(100)
	mgr, _ := NewManager(store, rl)

	r, _ := mgr.CreateRollout("gpt-4o", []string{"openai"}, "azure", []int{5, 100}, 5*time.Minute, 5.0, 3000)

	if err := mgr.PauseRollout(r.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := mgr.GetRollout(r.ID)
	if got.State != StatePaused {
		t.Errorf("expected paused, got %s", got.State)
	}

	if err := mgr.ResumeRollout(r.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = mgr.GetRollout(r.ID)
	if got.State != StateRunning {
		t.Errorf("expected running, got %s", got.State)
	}
}

func TestManualRollback(t *testing.T) {
	store := NewMemoryStore()
	rl := admin.NewRequestLog(100)
	mgr, _ := NewManager(store, rl)

	r, _ := mgr.CreateRollout("gpt-4o", []string{"openai"}, "azure", []int{5, 100}, 5*time.Minute, 5.0, 3000)

	if err := mgr.RollbackRollout(r.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := mgr.GetRollout(r.ID)
	if got.State != StateRolledBack {
		t.Errorf("expected rolled_back, got %s", got.State)
	}
	if got.CurrentPercentage != 0 {
		t.Errorf("expected 0%%, got %d%%", got.CurrentPercentage)
	}
}

func TestActiveRollout(t *testing.T) {
	store := NewMemoryStore()
	rl := admin.NewRequestLog(100)
	mgr, _ := NewManager(store, rl)

	// No active rollout
	if mgr.ActiveRollout("gpt-4o") != nil {
		t.Error("expected nil for no active rollout")
	}

	mgr.CreateRollout("gpt-4o", []string{"openai"}, "azure", []int{5, 100}, 5*time.Minute, 5.0, 3000)

	active := mgr.ActiveRollout("gpt-4o")
	if active == nil {
		t.Fatal("expected active rollout")
	}
	if active.CanaryProvider != "azure" {
		t.Errorf("expected canary azure, got %s", active.CanaryProvider)
	}
}

func TestInvalidStateTransitions(t *testing.T) {
	store := NewMemoryStore()
	rl := admin.NewRequestLog(100)
	mgr, _ := NewManager(store, rl)

	r, _ := mgr.CreateRollout("gpt-4o", []string{"openai"}, "azure", []int{5, 100}, 5*time.Minute, 5.0, 3000)

	// Can't resume a running rollout
	if err := mgr.ResumeRollout(r.ID); err == nil {
		t.Error("expected error resuming running rollout")
	}

	// Rollback then try to pause
	mgr.RollbackRollout(r.ID)
	if err := mgr.PauseRollout(r.ID); err == nil {
		t.Error("expected error pausing rolled-back rollout")
	}
}
```

- [ ] **Step 3: Build and run tests**

```bash
go build ./...
go test ./internal/rollout/ -v -count=1
```

Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/rollout/manager.go internal/rollout/manager_test.go
git commit -m "Implement rollout manager with lifecycle operations and background evaluation"
```

---

### Task 5: Integrate canary routing into router

**Files:**
- Modify: `internal/router/router.go`
- Modify: `internal/router/router_test.go`

- [ ] **Step 1: Add rollout manager to Router**

In `internal/router/router.go`, add the rollout integration. The Router needs a reference to the rollout manager. Add a `SetRolloutManager` method and modify `Route()` and `RouteStream()`:

```go
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
	Pattern   string
	Providers []string
	Strategy  Strategy
}

type Router struct {
	routes         []Route
	registry       *provider.Registry
	circuitBreaker *CircuitBreaker
	rolloutMgr     *rollout.Manager
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

func (r *Router) SetRolloutManager(mgr *rollout.Manager) {
	r.rolloutMgr = mgr
}

// RoutedResult contains the response and the provider that served it.
type RoutedResult struct {
	Response *types.ChatCompletionResponse
	Provider string
}

func (r *Router) Route(ctx context.Context, req *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error) {
	result, err := r.routeWithProvider(ctx, req)
	if err != nil {
		return nil, err
	}
	return result.Response, nil
}

// RouteWithProvider routes and returns both the response and which provider served it.
func (r *Router) RouteWithProvider(ctx context.Context, req *types.ChatCompletionRequest) (*RoutedResult, error) {
	return r.routeWithProvider(ctx, req)
}

func (r *Router) routeWithProvider(ctx context.Context, req *types.ChatCompletionRequest) (*RoutedResult, error) {
	// Check for active canary rollout
	if r.rolloutMgr != nil {
		if active := r.rolloutMgr.ActiveRollout(req.Model); active != nil {
			return r.routeCanary(ctx, req, active)
		}
	}

	providers, err := r.resolveProviders(req.Model)
	if err != nil {
		return nil, err
	}

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

func (r *Router) routeCanary(ctx context.Context, req *types.ChatCompletionRequest, active *rollout.Rollout) (*RoutedResult, error) {
	// Weighted random: route to canary if random < percentage
	if rand.Intn(100) < active.CurrentPercentage {
		// Try canary provider
		p, err := r.registry.Get(active.CanaryProvider)
		if err == nil && !r.circuitBreaker.IsOpen(p.Name()) {
			resp, err := p.ChatCompletion(ctx, req)
			if err == nil {
				r.circuitBreaker.RecordSuccess(p.Name())
				return &RoutedResult{Response: resp, Provider: p.Name()}, nil
			}
			r.circuitBreaker.RecordFailure(p.Name())
		}
	}

	// Baseline routing (normal path)
	providers, err := r.resolveProviders(req.Model)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, p := range providers {
		if p.Name() == active.CanaryProvider {
			continue // skip canary in baseline path
		}
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
```

- [ ] **Step 2: Add canary routing tests**

Add to `internal/router/router_test.go`:

```go
func TestCanaryRouting(t *testing.T) {
	// This test verifies that canary routing splits traffic correctly.
	// With a 50% canary, roughly half the requests should go to canary.
	reg := provider.NewRegistry()
	reg.Register(provider.NewMockProvider("openai", 10*time.Millisecond))
	reg.Register(provider.NewMockProvider("azure", 10*time.Millisecond))

	cfg := []config.RouteConfig{
		{Match: config.RouteMatch{Model: "gpt-*"}, Providers: []string{"openai", "azure"}, Strategy: "priority"},
	}

	rt := NewRouter(cfg, reg)

	// Create a rollout manager with in-memory store
	store := rollout.NewMemoryStore()
	rl := admin.NewRequestLog(500)
	mgr, err := rollout.NewManager(store, rl)
	if err != nil {
		t.Fatal(err)
	}
	rt.SetRolloutManager(mgr)

	// Create a 50% canary rollout
	_, err = mgr.CreateRollout("gpt-4o", []string{"openai"}, "azure", []int{50, 100}, 5*time.Minute, 5.0, 3000)
	if err != nil {
		t.Fatal(err)
	}

	// Route 100 requests and count how many go to canary
	canaryCount := 0
	for i := 0; i < 100; i++ {
		result, err := rt.RouteWithProvider(context.Background(), &types.ChatCompletionRequest{
			Model:    "gpt-4o",
			Messages: []types.Message{{Role: "user", Content: "test"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Provider == "azure" {
			canaryCount++
		}
	}

	// With 50% canary, expect 30-70 canary requests (statistical tolerance)
	if canaryCount < 20 || canaryCount > 80 {
		t.Errorf("expected ~50%% canary routing, got %d/100", canaryCount)
	}
}

func TestNoCanaryWithoutRollout(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(provider.NewMockProvider("openai", 10*time.Millisecond))

	cfg := []config.RouteConfig{
		{Match: config.RouteMatch{Model: "gpt-*"}, Providers: []string{"openai"}, Strategy: "priority"},
	}

	rt := NewRouter(cfg, reg)
	// No rollout manager set — should route normally

	result, err := rt.RouteWithProvider(context.Background(), &types.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []types.Message{{Role: "user", Content: "test"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "openai" {
		t.Errorf("expected openai, got %s", result.Provider)
	}
}
```

- [ ] **Step 3: Build and run tests**

```bash
go build ./...
go test ./internal/router/ -v -count=1
go test ./... -count=1
```

Expected: All tests pass including new canary routing tests.

- [ ] **Step 4: Commit**

```bash
git add internal/router/router.go internal/router/router_test.go
git commit -m "Integrate canary rollout routing with weighted random provider selection"
```

---

### Task 6: Add rollout admin API endpoints

**Files:**
- Modify: `internal/admin/admin.go`

- [ ] **Step 1: Add rollout manager to admin server and wire endpoints**

In `internal/admin/admin.go`, add the rollout manager field, update NewServer, and add all 6 rollout endpoints:

Add `rolloutMgr *rollout.Manager` to the Server struct. Update NewServer signature. Add routes in Router(). Add handler methods: `rolloutsListHandler`, `rolloutsCreateHandler`, `rolloutGetHandler`, `rolloutPauseHandler`, `rolloutResumeHandler`, `rolloutRollbackHandler`.

The admin server needs to import `internal/rollout` and `github.com/go-chi/chi/v5` for URL params.

Add these routes in the `Router()` method:

```go
r.Get("/admin/v1/rollouts", s.rolloutsListHandler)
r.Post("/admin/v1/rollouts", s.rolloutsCreateHandler)
r.Get("/admin/v1/rollouts/{id}", s.rolloutGetHandler)
r.Post("/admin/v1/rollouts/{id}/pause", s.rolloutPauseHandler)
r.Post("/admin/v1/rollouts/{id}/resume", s.rolloutResumeHandler)
r.Post("/admin/v1/rollouts/{id}/rollback", s.rolloutRollbackHandler)
```

The create handler parses JSON body with fields: `route_model`, `canary_provider`, `stages`, `observation_window`, `error_threshold`, `latency_p95_threshold`. It calls `m.rolloutMgr.CreateRollout(...)`.

The get handler returns rollout details plus live metrics from `m.rolloutMgr.GetMetrics(r)`.

Pause/resume/rollback handlers call the corresponding manager methods.

- [ ] **Step 2: Build and verify**

```bash
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add internal/admin/admin.go
git commit -m "Add rollout admin API: list, create, get, pause, resume, rollback endpoints"
```

---

### Task 7: Wire rollout manager into gateway startup

**Files:**
- Modify: `cmd/aegisflow/main.go`

- [ ] **Step 1: Initialize rollout manager in main.go**

After creating the router and before starting servers:

1. Create rollout store (PostgreSQL if available, else memory)
2. Create rollout manager with the store and request log
3. Set rollout manager on the router
4. Pass rollout manager to admin server
5. Start the manager's evaluation loop
6. Defer Stop on shutdown

```go
// Rollout manager
var rolloutStore rollout.Store
if pgStore != nil {
    rolloutStore = rollout.NewPostgresStore(pgStore.DB())
} else {
    rolloutStore = rollout.NewMemoryStore()
}
rolloutMgr, err := rollout.NewManager(rolloutStore, reqLog)
if err != nil {
    log.Printf("rollout manager init failed: %v", err)
} else {
    rt.SetRolloutManager(rolloutMgr)
    rolloutMgr.Start()
    defer rolloutMgr.Stop()
    log.Printf("rollout manager started")
}
```

Note: Need to expose `pgStore.DB()` method to get the `*sql.DB` for the rollout store. Add to `internal/storage/postgres.go`:

```go
func (s *PostgresStore) DB() *sql.DB {
    return s.db
}
```

Update admin server constructor to accept rollout manager.

- [ ] **Step 2: Build and run full test suite**

```bash
go build ./...
go test ./... -count=1 -race
```

Expected: All tests pass.

- [ ] **Step 3: Commit**

```bash
git add cmd/aegisflow/main.go internal/storage/postgres.go internal/admin/admin.go
git commit -m "Wire rollout manager into gateway startup with PostgreSQL/memory store"
```

---

### Task 8: Add Rollouts dashboard page

**Files:**
- Modify: `internal/admin/dashboard.html`

- [ ] **Step 1: Add Rollouts nav item**

Add after the Cache nav item, under Monitoring:

```html
<div class="nav-item" data-page="rollouts" onclick="switchPage('rollouts')">
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83"/></svg>
  Rollouts
</div>
```

- [ ] **Step 2: Add Rollouts page HTML**

Add the page div with stat cards (Active, Completed, Rolled Back), active rollout cards with progress bars and action buttons, and a history table.

- [ ] **Step 3: Add fetchRollouts JavaScript**

Add `fetchRollouts()` function that calls `/admin/v1/rollouts`, renders active rollouts with progress visualization and pause/rollback buttons, and auto-refreshes every 3 seconds when on the page.

Add to `switchPage`:
```javascript
if (page === 'rollouts') fetchRollouts();
```

- [ ] **Step 4: Build and verify**

```bash
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add internal/admin/dashboard.html
git commit -m "Add Rollouts dashboard page with live progress, metrics, and action buttons"
```

---

### Task 9: End-to-end verification

- [ ] **Step 1: Run full test suite with race detector**

```bash
go test ./... -v -count=1 -race
```

Expected: All tests pass.

- [ ] **Step 2: Manual smoke test**

Start the gateway, create a rollout via API, send requests, verify traffic split in live feed, verify auto-promotion or rollback based on health.

```bash
go build -o bin/aegisflow ./cmd/aegisflow
./bin/aegisflow --config configs/aegisflow.yaml &

# Create a rollout
curl -s -X POST http://localhost:8081/admin/v1/rollouts \
  -H "Content-Type: application/json" \
  -d '{"route_model":"mock","canary_provider":"mock","stages":[25,50,100],"observation_window":"10s","error_threshold":5.0,"latency_p95_threshold":3000}'

# List rollouts
curl -s http://localhost:8081/admin/v1/rollouts | python3 -m json.tool

# Send traffic
for i in $(seq 1 20); do
  curl -s -X POST http://localhost:8080/v1/chat/completions \
    -H "X-API-Key: aegis-test-default-001" \
    -H "Content-Type: application/json" \
    -d '{"model":"mock","messages":[{"role":"user","content":"test"}]}' > /dev/null
done

# Check dashboard at http://localhost:8081/dashboard → Rollouts page
```

- [ ] **Step 3: Final commit**

```bash
git add -A
git commit -m "Phase 3A complete: canary rollouts with auto-promotion, health evaluation, and dashboard"
```
