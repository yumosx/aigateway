# WASM Policy Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow users to write custom policy filters as WASM modules and load them via config without recompiling the gateway.

**Architecture:** A `WasmFilter` in `internal/policy/` implements the existing `Filter` interface using wazero (pure Go WASM runtime). WASM plugins export `check`, `alloc`, `get_result_ptr`, `get_result_len` functions. The host writes content + JSON metadata into WASM memory, the plugin processes it and returns a violation result. Config-level `action` always wins over plugin-returned action.

**Tech Stack:** Go, wazero (WASM runtime), TinyGo (example plugin)

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/policy/filter_wasm.go` | Create | `WasmFilter` struct implementing `Filter` interface, module loading, `Check()` execution |
| `internal/policy/filter_wasm_test.go` | Create | Unit tests for all WASM filter behaviors |
| `internal/policy/testdata/block.wasm` | Create | Test WASM binary that blocks content containing "forbidden" |
| `internal/policy/testdata/allow.wasm` | Create | Test WASM binary that always allows |
| `internal/policy/testdata/bad_result.wasm` | Create | Test WASM binary that returns invalid JSON |
| `internal/config/config.go` | Modify | Add `Path`, `Timeout`, `OnError` fields to `PolicyConfig` |
| `cmd/aegisflow/main.go` | Modify | Add `case "wasm"` in `initPolicyEngine()` |
| `examples/wasm-plugin/main.go` | Create | TinyGo example plugin source |
| `examples/wasm-plugin/Makefile` | Create | Build instructions for example plugin |
| `examples/wasm-plugin/README.md` | Create | ABI docs and usage instructions |
| `go.mod` / `go.sum` | Modify | Add wazero dependency |

---

### Task 1: Add wazero dependency and config fields

**Files:**
- Modify: `go.mod`
- Modify: `internal/config/config.go:123-129`

- [ ] **Step 1: Add wazero dependency**

Run:
```bash
cd /Users/saivedanthava/Desktop/AegisFlow
go get github.com/tetratelabs/wazero@latest
```

- [ ] **Step 2: Add new fields to PolicyConfig**

In `internal/config/config.go`, replace the `PolicyConfig` struct:

```go
type PolicyConfig struct {
	Name     string        `yaml:"name"`
	Type     string        `yaml:"type"`
	Action   string        `yaml:"action"`
	Keywords []string      `yaml:"keywords"`
	Patterns []string      `yaml:"patterns"`
	Path     string        `yaml:"path"`
	Timeout  time.Duration `yaml:"timeout"`
	OnError  string        `yaml:"on_error"`
}
```

- [ ] **Step 3: Build to verify no breakage**

Run:
```bash
go build ./...
```

Expected: Success, no errors.

- [ ] **Step 4: Run existing tests**

Run:
```bash
go test ./... -count=1
```

Expected: All existing tests pass.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/config/config.go
git commit -m "Add wazero dependency and WASM config fields to PolicyConfig"
```

---

### Task 2: Build test WASM binaries

We need small `.wasm` binaries for testing. These are written in raw WAT (WebAssembly Text Format) and compiled with `wazero`'s built-in assembler, or hand-crafted as byte slices in Go tests. Since we want CI to work without TinyGo installed, we'll embed the test binaries as Go byte slices built from WAT.

**Files:**
- Create: `internal/policy/testdata/testplugins.go`

- [ ] **Step 1: Create testdata directory**

Run:
```bash
mkdir -p /Users/saivedanthava/Desktop/AegisFlow/internal/policy/testdata
```

- [ ] **Step 2: Write test plugin builder**

Create `internal/policy/testdata/testplugins.go`. This file provides functions that return pre-compiled WASM bytes for testing. The binaries are built using wazero's binary format helpers.

```go
package testdata

import (
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental/wasm"
)
```

Actually — since hand-crafting WASM bytecode is error-prone and hard to maintain, a better approach: write the test plugins as small TinyGo files and check in the compiled `.wasm` binaries. But that requires TinyGo in CI.

**Revised approach:** Write a single Go test helper that uses wazero's `wasm.NewModuleBuilder` to create in-memory test modules. This avoids any external toolchain dependency.

Create `internal/policy/wasm_testhelper_test.go`:

```go
package policy

import (
	"context"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// buildTestWasmModule creates a minimal WASM module in-memory for testing.
// The module implements the required ABI: alloc, check, get_result_ptr, get_result_len.
// behavior controls what check() does:
//   - "block" → always returns 1 (violation), result = {"action":"block","message":"blocked by test plugin"}
//   - "allow" → always returns 0
//   - "bad_result" → returns 1 but result is invalid JSON
//   - "panic" → calls unreachable instruction
func buildTestPlugin(behavior string) ([]byte, error) {
	// We'll use a real TinyGo-compiled .wasm checked into testdata/ instead.
	// See Step 3 for the actual test binaries.
	return nil, nil
}
```

Wait — this approach has a circular problem. Let me reconsider.

**Final approach:** Write the test WASM plugins as simple Go programs compiled with `GOOS=wasip1 GOARCH=wasm` (available since Go 1.21). This requires no TinyGo — just the standard Go toolchain. The compiled `.wasm` files are checked into `testdata/`.

- [ ] **Step 2 (revised): Create test plugin — "block" behavior**

Create `internal/policy/testdata/block/main.go`:

```go
package main

import (
	"encoding/json"
	"strings"
	"unsafe"
)

// Global state for result passing
var resultBuf []byte

//export alloc
func alloc(size uint32) *byte {
	buf := make([]byte, size)
	return &buf[0]
}

//export check
func check(contentPtr *byte, contentLen uint32, metaPtr *byte, metaLen uint32) int32 {
	content := unsafe.String(contentPtr, contentLen)

	if strings.Contains(strings.ToLower(content), "forbidden") {
		result, _ := json.Marshal(map[string]string{
			"action":  "block",
			"message": "content contains forbidden word",
		})
		resultBuf = result
		return 1
	}
	return 0
}

//export get_result_ptr
func getResultPtr() *byte {
	if len(resultBuf) == 0 {
		return nil
	}
	return &resultBuf[0]
}

//export get_result_len
func getResultLen() uint32 {
	return uint32(len(resultBuf))
}

func main() {}
```

- [ ] **Step 3: Create test plugin — "allow" behavior**

Create `internal/policy/testdata/allow/main.go`:

```go
package main

import "unsafe"

var resultBuf []byte

//export alloc
func alloc(size uint32) *byte {
	buf := make([]byte, size)
	return &buf[0]
}

//export check
func check(contentPtr *byte, contentLen uint32, metaPtr *byte, metaLen uint32) int32 {
	_ = unsafe.String(contentPtr, contentLen)
	return 0 // always allow
}

//export get_result_ptr
func getResultPtr() *byte {
	if len(resultBuf) == 0 {
		return nil
	}
	return &resultBuf[0]
}

//export get_result_len
func getResultLen() uint32 {
	return uint32(len(resultBuf))
}

func main() {}
```

- [ ] **Step 4: Create test plugin — "bad_result" behavior**

Create `internal/policy/testdata/bad_result/main.go`:

```go
package main

import "unsafe"

var resultBuf []byte

//export alloc
func alloc(size uint32) *byte {
	buf := make([]byte, size)
	return &buf[0]
}

//export check
func check(contentPtr *byte, contentLen uint32, metaPtr *byte, metaLen uint32) int32 {
	_ = unsafe.String(contentPtr, contentLen)
	resultBuf = []byte("not valid json {{{")
	return 1
}

//export get_result_ptr
func getResultPtr() *byte {
	if len(resultBuf) == 0 {
		return nil
	}
	return &resultBuf[0]
}

//export get_result_len
func getResultLen() uint32 {
	return uint32(len(resultBuf))
}

func main() {}
```

- [ ] **Step 5: Compile test WASM binaries**

Run:
```bash
cd /Users/saivedanthava/Desktop/AegisFlow
GOOS=wasip1 GOARCH=wasm go build -o internal/policy/testdata/block.wasm ./internal/policy/testdata/block/
GOOS=wasip1 GOARCH=wasm go build -o internal/policy/testdata/allow.wasm ./internal/policy/testdata/allow/
GOOS=wasip1 GOARCH=wasm go build -o internal/policy/testdata/bad_result.wasm ./internal/policy/testdata/bad_result/
```

Expected: Three `.wasm` files created in `testdata/`.

- [ ] **Step 6: Commit test binaries and source**

```bash
git add internal/policy/testdata/
git commit -m "Add test WASM plugin binaries for policy filter tests"
```

---

### Task 3: Implement WasmFilter

**Files:**
- Create: `internal/policy/filter_wasm.go`

- [ ] **Step 1: Write the WasmFilter implementation**

Create `internal/policy/filter_wasm.go`:

```go
package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// WasmMetadata holds request context passed to WASM plugins.
type WasmMetadata struct {
	TenantID string `json:"tenant_id"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Phase    string `json:"phase"`
}

// WasmFilter runs a WASM module as a policy filter.
type WasmFilter struct {
	name    string
	action  Action
	onError string
	timeout time.Duration

	runtime wazero.Runtime
	module  api.Module
	mu      sync.Mutex

	// metadata is set before each Check call by the caller
	metadata *WasmMetadata
}

// wasmResult is the JSON structure returned by the plugin on violation.
type wasmResult struct {
	Action  string `json:"action"`
	Message string `json:"message"`
}

// NewWasmFilter loads a WASM module from the given path and prepares it for execution.
func NewWasmFilter(name string, action Action, wasmPath string, timeout time.Duration, onError string) (*WasmFilter, error) {
	if timeout == 0 {
		timeout = 100 * time.Millisecond
	}
	if onError == "" {
		onError = "block"
	}

	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("reading wasm file %s: %w", wasmPath, err)
	}

	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)

	// Instantiate WASI for basic I/O support (needed by Go/TinyGo compiled WASM)
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)

	module, err := rt.Instantiate(ctx, wasmBytes)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("instantiating wasm module %s: %w", wasmPath, err)
	}

	// Validate required exports
	for _, fname := range []string{"check", "alloc", "get_result_ptr", "get_result_len"} {
		if module.ExportedFunction(fname) == nil {
			module.Close(ctx)
			rt.Close(ctx)
			return nil, fmt.Errorf("wasm module %s missing required export: %s", wasmPath, fname)
		}
	}

	return &WasmFilter{
		name:    name,
		action:  action,
		onError: onError,
		timeout: timeout,
		runtime: rt,
		module:  module,
	}, nil
}

// NewWasmFilterFromBytes loads a WASM module from bytes (used in tests).
func NewWasmFilterFromBytes(name string, action Action, wasmBytes []byte, timeout time.Duration, onError string) (*WasmFilter, error) {
	if timeout == 0 {
		timeout = 100 * time.Millisecond
	}
	if onError == "" {
		onError = "block"
	}

	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)

	wasi_snapshot_preview1.MustInstantiate(ctx, rt)

	module, err := rt.Instantiate(ctx, wasmBytes)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("instantiating wasm module: %w", err)
	}

	for _, fname := range []string{"check", "alloc", "get_result_ptr", "get_result_len"} {
		if module.ExportedFunction(fname) == nil {
			module.Close(ctx)
			rt.Close(ctx)
			return nil, fmt.Errorf("wasm module missing required export: %s", fname)
		}
	}

	return &WasmFilter{
		name:    name,
		action:  action,
		onError: onError,
		timeout: timeout,
		runtime: rt,
		module:  module,
	}, nil
}

func (f *WasmFilter) Name() string   { return f.name }
func (f *WasmFilter) Action() Action { return f.action }

// SetMetadata sets the request metadata for the next Check call.
func (f *WasmFilter) SetMetadata(meta *WasmMetadata) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metadata = meta
}

func (f *WasmFilter) Check(content string) *Violation {
	f.mu.Lock()
	meta := f.metadata
	f.mu.Unlock()

	if meta == nil {
		meta = &WasmMetadata{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	violation, err := f.callCheck(ctx, content, meta)
	if err != nil {
		log.Printf("wasm plugin %s error: %v", f.name, err)
		if f.onError == "block" {
			return &Violation{
				PolicyName: f.name,
				Action:     f.action,
				Message:    fmt.Sprintf("wasm plugin error: %s: %v", f.name, err),
			}
		}
		return nil // on_error: allow
	}

	return violation
}

func (f *WasmFilter) callCheck(ctx context.Context, content string, meta *WasmMetadata) (*Violation, error) {
	allocFn := f.module.ExportedFunction("alloc")
	checkFn := f.module.ExportedFunction("check")
	getResultPtrFn := f.module.ExportedFunction("get_result_ptr")
	getResultLenFn := f.module.ExportedFunction("get_result_len")

	// Marshal metadata to JSON
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshaling metadata: %w", err)
	}

	contentBytes := []byte(content)

	// Allocate memory for content in WASM
	contentResults, err := allocFn.Call(ctx, uint64(len(contentBytes)))
	if err != nil {
		return nil, fmt.Errorf("alloc for content: %w", err)
	}
	contentPtr := uint32(contentResults[0])

	// Write content to WASM memory
	if !f.module.Memory().Write(contentPtr, contentBytes) {
		return nil, fmt.Errorf("writing content to wasm memory")
	}

	// Allocate memory for metadata in WASM
	metaResults, err := allocFn.Call(ctx, uint64(len(metaJSON)))
	if err != nil {
		return nil, fmt.Errorf("alloc for metadata: %w", err)
	}
	metaPtr := uint32(metaResults[0])

	// Write metadata to WASM memory
	if !f.module.Memory().Write(metaPtr, metaJSON) {
		return nil, fmt.Errorf("writing metadata to wasm memory")
	}

	// Call check function
	checkResults, err := checkFn.Call(ctx,
		uint64(contentPtr), uint64(len(contentBytes)),
		uint64(metaPtr), uint64(len(metaJSON)),
	)
	if err != nil {
		return nil, fmt.Errorf("calling check: %w", err)
	}

	result := int32(checkResults[0])
	if result == 0 {
		return nil, nil // no violation
	}

	// Read result from WASM memory
	ptrResults, err := getResultPtrFn.Call(ctx)
	if err != nil {
		return nil, fmt.Errorf("calling get_result_ptr: %w", err)
	}
	lenResults, err := getResultLenFn.Call(ctx)
	if err != nil {
		return nil, fmt.Errorf("calling get_result_len: %w", err)
	}

	resultPtr := uint32(ptrResults[0])
	resultLen := uint32(lenResults[0])

	resultBytes, ok := f.module.Memory().Read(resultPtr, resultLen)
	if !ok {
		return nil, fmt.Errorf("reading result from wasm memory")
	}

	var wr wasmResult
	if err := json.Unmarshal(resultBytes, &wr); err != nil {
		return nil, fmt.Errorf("parsing wasm result JSON: %w", err)
	}

	return &Violation{
		PolicyName: f.name,
		Action:     f.action, // config wins over plugin action
		Message:    wr.Message,
	}, nil
}

// Close releases the WASM runtime resources.
func (f *WasmFilter) Close() error {
	ctx := context.Background()
	f.module.Close(ctx)
	return f.runtime.Close(ctx)
}
```

- [ ] **Step 2: Build to verify compilation**

Run:
```bash
go build ./internal/policy/...
```

Expected: Success.

- [ ] **Step 3: Commit**

```bash
git add internal/policy/filter_wasm.go
git commit -m "Implement WasmFilter with wazero runtime for WASM policy plugins"
```

---

### Task 4: Write WasmFilter tests

**Files:**
- Create: `internal/policy/filter_wasm_test.go`

- [ ] **Step 1: Write the tests**

Create `internal/policy/filter_wasm_test.go`:

```go
package policy

import (
	"os"
	"testing"
	"time"
)

func loadTestWasm(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("failed to load test wasm %s: %v", name, err)
	}
	return data
}

func TestWasmFilterBlocks(t *testing.T) {
	wasm := loadTestWasm(t, "block.wasm")
	f, err := NewWasmFilterFromBytes("test-block", ActionBlock, wasm, time.Second, "block")
	if err != nil {
		t.Fatalf("failed to create wasm filter: %v", err)
	}
	defer f.Close()

	f.SetMetadata(&WasmMetadata{
		TenantID: "test-tenant",
		Model:    "gpt-4o",
		Provider: "openai",
		Phase:    "input",
	})

	v := f.Check("this contains the forbidden word")
	if v == nil {
		t.Fatal("expected violation for content containing 'forbidden'")
	}
	if v.PolicyName != "test-block" {
		t.Errorf("expected policy name 'test-block', got '%s'", v.PolicyName)
	}
	if v.Action != ActionBlock {
		t.Errorf("expected action block, got %s", v.Action)
	}
	if v.Message == "" {
		t.Error("expected non-empty violation message")
	}
}

func TestWasmFilterAllows(t *testing.T) {
	wasm := loadTestWasm(t, "allow.wasm")
	f, err := NewWasmFilterFromBytes("test-allow", ActionBlock, wasm, time.Second, "block")
	if err != nil {
		t.Fatalf("failed to create wasm filter: %v", err)
	}
	defer f.Close()

	f.SetMetadata(&WasmMetadata{TenantID: "t1", Model: "gpt-4o", Provider: "openai", Phase: "input"})

	v := f.Check("this is perfectly fine content")
	if v != nil {
		t.Error("expected no violation for clean content")
	}
}

func TestWasmFilterBlockAllowsCleanContent(t *testing.T) {
	wasm := loadTestWasm(t, "block.wasm")
	f, err := NewWasmFilterFromBytes("test-block", ActionBlock, wasm, time.Second, "block")
	if err != nil {
		t.Fatalf("failed to create wasm filter: %v", err)
	}
	defer f.Close()

	f.SetMetadata(&WasmMetadata{TenantID: "t1", Model: "gpt-4o", Provider: "openai", Phase: "input"})

	v := f.Check("this is normal content without bad words")
	if v != nil {
		t.Error("expected no violation for clean content")
	}
}

func TestWasmFilterBadResultOnErrorBlock(t *testing.T) {
	wasm := loadTestWasm(t, "bad_result.wasm")
	f, err := NewWasmFilterFromBytes("test-bad", ActionBlock, wasm, time.Second, "block")
	if err != nil {
		t.Fatalf("failed to create wasm filter: %v", err)
	}
	defer f.Close()

	f.SetMetadata(&WasmMetadata{TenantID: "t1", Model: "gpt-4o", Provider: "openai", Phase: "input"})

	v := f.Check("anything")
	if v == nil {
		t.Fatal("expected violation from on_error:block when plugin returns bad JSON")
	}
	if v.Action != ActionBlock {
		t.Errorf("expected block action, got %s", v.Action)
	}
}

func TestWasmFilterBadResultOnErrorAllow(t *testing.T) {
	wasm := loadTestWasm(t, "bad_result.wasm")
	f, err := NewWasmFilterFromBytes("test-bad-allow", ActionBlock, wasm, time.Second, "allow")
	if err != nil {
		t.Fatalf("failed to create wasm filter: %v", err)
	}
	defer f.Close()

	f.SetMetadata(&WasmMetadata{TenantID: "t1", Model: "gpt-4o", Provider: "openai", Phase: "input"})

	v := f.Check("anything")
	if v != nil {
		t.Error("expected no violation when on_error is 'allow'")
	}
}

func TestWasmFilterMissingExports(t *testing.T) {
	// A minimal valid WASM module with no exports (just the magic header + empty module)
	emptyWasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	_, err := NewWasmFilterFromBytes("test-empty", ActionBlock, emptyWasm, time.Second, "block")
	if err == nil {
		t.Fatal("expected error for WASM module missing required exports")
	}
}

func TestWasmFilterNilMetadata(t *testing.T) {
	wasm := loadTestWasm(t, "allow.wasm")
	f, err := NewWasmFilterFromBytes("test-nil-meta", ActionBlock, wasm, time.Second, "block")
	if err != nil {
		t.Fatalf("failed to create wasm filter: %v", err)
	}
	defer f.Close()

	// Don't set metadata — should still work with empty defaults
	v := f.Check("normal content")
	if v != nil {
		t.Error("expected no violation with nil metadata")
	}
}

func TestWasmFilterConfigActionOverridesPlugin(t *testing.T) {
	wasm := loadTestWasm(t, "block.wasm")
	// Config says "warn" even though plugin returns "block" in its result JSON
	f, err := NewWasmFilterFromBytes("test-override", ActionWarn, wasm, time.Second, "block")
	if err != nil {
		t.Fatalf("failed to create wasm filter: %v", err)
	}
	defer f.Close()

	f.SetMetadata(&WasmMetadata{TenantID: "t1", Model: "gpt-4o", Provider: "openai", Phase: "input"})

	v := f.Check("this is forbidden content")
	if v == nil {
		t.Fatal("expected violation")
	}
	if v.Action != ActionWarn {
		t.Errorf("expected config action 'warn' to override plugin, got '%s'", v.Action)
	}
}

func TestWasmFilterInEngine(t *testing.T) {
	wasm := loadTestWasm(t, "block.wasm")
	wasmFilter, err := NewWasmFilterFromBytes("wasm-jailbreak", ActionBlock, wasm, time.Second, "block")
	if err != nil {
		t.Fatalf("failed to create wasm filter: %v", err)
	}
	defer wasmFilter.Close()

	wasmFilter.SetMetadata(&WasmMetadata{TenantID: "t1", Model: "gpt-4o", Provider: "openai", Phase: "input"})

	// Built-in keyword filter + WASM filter in the same engine
	kwFilter := NewKeywordFilter("kw-test", ActionBlock, []string{"blocked_word"})
	engine := NewEngine([]Filter{kwFilter, wasmFilter}, nil)

	// Keyword filter should catch this
	v, _ := engine.CheckInput("this has blocked_word in it")
	if v == nil || v.PolicyName != "kw-test" {
		t.Error("expected keyword filter to catch 'blocked_word'")
	}

	// WASM filter should catch this
	v, _ = engine.CheckInput("this has forbidden in it")
	if v == nil || v.PolicyName != "wasm-jailbreak" {
		t.Errorf("expected wasm filter to catch 'forbidden', got %v", v)
	}

	// Neither should catch this
	v, _ = engine.CheckInput("this is perfectly clean")
	if v != nil {
		t.Error("expected no violation for clean input")
	}
}
```

- [ ] **Step 2: Run the tests**

Run:
```bash
cd /Users/saivedanthava/Desktop/AegisFlow
go test ./internal/policy/ -v -run TestWasm -count=1
```

Expected: All `TestWasm*` tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/policy/filter_wasm_test.go
git commit -m "Add comprehensive tests for WasmFilter"
```

---

### Task 5: Wire WASM filters into gateway startup

**Files:**
- Modify: `cmd/aegisflow/main.go:246-271`

- [ ] **Step 1: Add wasm case to initPolicyEngine**

In `cmd/aegisflow/main.go`, update the `initPolicyEngine` function. Add a `case "wasm"` in both input and output filter loops:

```go
func initPolicyEngine(cfg *config.Config) *policy.Engine {
	var inputFilters []policy.Filter
	for _, p := range cfg.Policies.Input {
		switch p.Type {
		case "keyword":
			inputFilters = append(inputFilters, policy.NewKeywordFilter(p.Name, policy.Action(p.Action), p.Keywords))
		case "regex":
			inputFilters = append(inputFilters, policy.NewRegexFilter(p.Name, policy.Action(p.Action), p.Patterns))
		case "pii":
			inputFilters = append(inputFilters, policy.NewPIIFilter(p.Name, policy.Action(p.Action), p.Patterns))
		case "wasm":
			timeout := p.Timeout
			if timeout == 0 {
				timeout = 100 * time.Millisecond
			}
			onError := p.OnError
			if onError == "" {
				onError = "block"
			}
			wf, err := policy.NewWasmFilter(p.Name, policy.Action(p.Action), p.Path, timeout, onError)
			if err != nil {
				log.Printf("failed to load wasm policy %s from %s: %v (skipping)", p.Name, p.Path, err)
				continue
			}
			inputFilters = append(inputFilters, wf)
			log.Printf("loaded wasm policy: %s (path: %s, action: %s, on_error: %s, timeout: %s)", p.Name, p.Path, p.Action, onError, timeout)
		}
	}

	var outputFilters []policy.Filter
	for _, p := range cfg.Policies.Output {
		switch p.Type {
		case "keyword":
			outputFilters = append(outputFilters, policy.NewKeywordFilter(p.Name, policy.Action(p.Action), p.Keywords))
		case "regex":
			outputFilters = append(outputFilters, policy.NewRegexFilter(p.Name, policy.Action(p.Action), p.Patterns))
		case "wasm":
			timeout := p.Timeout
			if timeout == 0 {
				timeout = 100 * time.Millisecond
			}
			onError := p.OnError
			if onError == "" {
				onError = "block"
			}
			wf, err := policy.NewWasmFilter(p.Name, policy.Action(p.Action), p.Path, timeout, onError)
			if err != nil {
				log.Printf("failed to load wasm policy %s from %s: %v (skipping)", p.Name, p.Path, err)
				continue
			}
			outputFilters = append(outputFilters, wf)
			log.Printf("loaded wasm policy: %s (path: %s, action: %s, on_error: %s, timeout: %s)", p.Name, p.Path, p.Action, onError, timeout)
		}
	}

	log.Printf("loaded %d input policies, %d output policies", len(inputFilters), len(outputFilters))
	return policy.NewEngine(inputFilters, outputFilters)
}
```

- [ ] **Step 2: Build the full project**

Run:
```bash
go build ./...
```

Expected: Success.

- [ ] **Step 3: Run all tests to verify no regression**

Run:
```bash
go test ./... -count=1
```

Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add cmd/aegisflow/main.go
git commit -m "Wire WASM policy filters into gateway startup"
```

---

### Task 6: Create example TinyGo plugin

**Files:**
- Create: `examples/wasm-plugin/main.go`
- Create: `examples/wasm-plugin/Makefile`
- Create: `examples/wasm-plugin/README.md`

- [ ] **Step 1: Create example plugin directory**

Run:
```bash
mkdir -p /Users/saivedanthava/Desktop/AegisFlow/examples/wasm-plugin
```

- [ ] **Step 2: Write example plugin source**

Create `examples/wasm-plugin/main.go`:

```go
// Example AegisFlow WASM policy plugin.
//
// This plugin blocks any message containing the word "forbidden".
// It demonstrates the full ABI contract that WASM plugins must implement.
//
// Build with standard Go:
//   GOOS=wasip1 GOARCH=wasm go build -o plugin.wasm .
//
// Or with TinyGo (smaller binary):
//   tinygo build -o plugin.wasm -target wasi .
package main

import (
	"encoding/json"
	"strings"
	"unsafe"
)

// resultBuf holds the violation result between check() and get_result_ptr/len calls.
var resultBuf []byte

// metadata is the JSON structure passed by the AegisFlow host.
type metadata struct {
	TenantID string `json:"tenant_id"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Phase    string `json:"phase"`
}

// alloc allocates memory in the WASM module for the host to write into.
// The host calls this before writing content and metadata.
//
//export alloc
func alloc(size uint32) *byte {
	buf := make([]byte, size)
	return &buf[0]
}

// check is the main entry point called by AegisFlow for each request.
// It receives pointers to content and metadata in WASM linear memory.
// Returns 0 for no violation, 1 for violation found.
//
//export check
func check(contentPtr *byte, contentLen uint32, metaPtr *byte, metaLen uint32) int32 {
	// Read content string from WASM memory
	content := unsafe.String(contentPtr, contentLen)

	// Read and parse metadata (optional — ignore errors for robustness)
	metaBytes := unsafe.Slice(metaPtr, metaLen)
	var meta metadata
	_ = json.Unmarshal(metaBytes, &meta)

	// Example policy: block content containing "forbidden"
	if strings.Contains(strings.ToLower(content), "forbidden") {
		result, _ := json.Marshal(map[string]string{
			"action":  "block",
			"message": "content contains forbidden word (tenant: " + meta.TenantID + ", model: " + meta.Model + ")",
		})
		resultBuf = result
		return 1
	}

	return 0
}

// get_result_ptr returns a pointer to the violation result in WASM memory.
// Called by the host after check() returns 1.
//
//export get_result_ptr
func getResultPtr() *byte {
	if len(resultBuf) == 0 {
		return nil
	}
	return &resultBuf[0]
}

// get_result_len returns the length of the violation result.
// Called by the host after check() returns 1.
//
//export get_result_len
func getResultLen() uint32 {
	return uint32(len(resultBuf))
}

func main() {}
```

- [ ] **Step 3: Write Makefile**

Create `examples/wasm-plugin/Makefile`:

```makefile
.PHONY: build clean

# Build with standard Go (no external toolchain needed)
build:
	GOOS=wasip1 GOARCH=wasm go build -o plugin.wasm .

# Build with TinyGo (smaller binary, ~50KB vs ~2MB)
build-tiny:
	tinygo build -o plugin.wasm -target wasi .

clean:
	rm -f plugin.wasm
```

- [ ] **Step 4: Write README**

Create `examples/wasm-plugin/README.md`:

```markdown
# AegisFlow WASM Policy Plugin Example

This is a reference implementation of an AegisFlow WASM policy plugin written in Go.

## Building

Standard Go (no extra tools needed):

    make build

TinyGo (smaller binary):

    make build-tiny

## Using

Copy the compiled `plugin.wasm` to your AegisFlow instance and add it to your config:

    policies:
      input:
        - name: "custom-filter"
          type: "wasm"
          action: "block"
          path: "plugins/plugin.wasm"
          timeout: 100ms
          on_error: "block"

## ABI Contract

Your WASM module must export these four functions:

| Export | Signature | Description |
|--------|-----------|-------------|
| `alloc` | `(size i32) → i32` | Allocate `size` bytes, return pointer. Host writes inputs here. |
| `check` | `(content_ptr i32, content_len i32, meta_ptr i32, meta_len i32) → i32` | Main check function. Return `0` (allow) or `1` (violation). |
| `get_result_ptr` | `() → i32` | Return pointer to result JSON after `check` returns `1`. |
| `get_result_len` | `() → i32` | Return length of result JSON. |

### Metadata JSON (input)

The host passes request metadata as JSON:

    {"tenant_id": "default", "model": "gpt-4o", "provider": "openai", "phase": "input"}

### Result JSON (output)

When `check` returns `1`, write a result to memory and expose via `get_result_ptr`/`get_result_len`:

    {"action": "block", "message": "description of why this was flagged"}

Note: The `action` in your result is informational. AegisFlow uses the `action` from the config, not from your plugin.

## Supported Languages

Any language that compiles to WASM with WASI support works:
- **Go** — `GOOS=wasip1 GOARCH=wasm go build`
- **TinyGo** — `tinygo build -target wasi`
- **Rust** — `cargo build --target wasm32-wasi`
- **AssemblyScript** — `asc main.ts --outFile plugin.wasm`
```

- [ ] **Step 5: Commit**

```bash
git add examples/wasm-plugin/
git commit -m "Add example WASM policy plugin with build instructions"
```

---

### Task 7: End-to-end verification

- [ ] **Step 1: Run full test suite**

Run:
```bash
cd /Users/saivedanthava/Desktop/AegisFlow
go test ./... -v -count=1 -race
```

Expected: All tests pass with race detector, including new WASM tests.

- [ ] **Step 2: Build example plugin**

Run:
```bash
cd /Users/saivedanthava/Desktop/AegisFlow/examples/wasm-plugin
make build
ls -la plugin.wasm
```

Expected: `plugin.wasm` file created.

- [ ] **Step 3: Test gateway loads WASM plugin from config**

Add to `configs/aegisflow.yaml` under `policies.input` (temporarily for testing):

```yaml
    - name: "example-wasm"
      type: "wasm"
      action: "block"
      path: "examples/wasm-plugin/plugin.wasm"
      timeout: 100ms
      on_error: "allow"
```

Then build and run:
```bash
cd /Users/saivedanthava/Desktop/AegisFlow
go build -o bin/aegisflow ./cmd/aegisflow
./bin/aegisflow --config configs/aegisflow.yaml
```

Expected in logs: `loaded wasm policy: example-wasm (path: examples/wasm-plugin/plugin.wasm, ...)`

Stop the server with Ctrl+C. Remove the test entry from `aegisflow.yaml`.

- [ ] **Step 4: Final commit**

```bash
git add -A
git commit -m "WASM policy plugin support — complete implementation"
```
