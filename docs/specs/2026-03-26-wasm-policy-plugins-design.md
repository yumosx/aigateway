# WASM Policy Plugin Support

**Date:** 2026-03-26
**Status:** Approved
**Scope:** Add WASM plugin support to AegisFlow's policy engine

## Overview

Users can write custom policy filters in any language that compiles to WASM (Rust, TinyGo, AssemblyScript), load them at runtime via config without recompiling the gateway, and have them participate in the same input/output policy pipeline as built-in filters.

The WASM runtime is **wazero** (pure Go, no CGO dependency).

## Architecture

A new `WasmFilter` type in `internal/policy/` implements the existing `Filter` interface. From the policy engine's perspective, a WASM filter is just another `Filter` — no changes to `engine.go`.

### Components

| Component | Path | Purpose |
|-----------|------|---------|
| `filter_wasm.go` | `internal/policy/` | Implements `Filter` interface, manages wazero runtime + module lifecycle |
| `wasm_host.go` | `internal/policy/` | Host functions exposed to WASM (memory allocation, result passing) |
| Config changes | `internal/config/config.go` | New fields: `Path`, `Timeout`, `OnError` on `PolicyConfig` |
| Init changes | `cmd/aegisflow/main.go` | Handle `case "wasm"` in `initPolicyEngine()` |
| Example plugin | `examples/wasm-plugin/` | TinyGo reference implementation with Makefile |

### What doesn't change

- `engine.go` — `WasmFilter` satisfies `Filter` interface, engine is unaware it's WASM
- Existing filters (keyword, regex, PII) — untouched
- Admin API / dashboard — no changes needed

## WASM Plugin ABI

### Exports required from plugin

```
check(content_ptr i32, content_len i32, meta_ptr i32, meta_len i32) → i32
alloc(size i32) → i32
get_result_ptr() → i32
get_result_len() → i32
```

### Function behavior

**`check`**: Main entry point. Host writes content (string) and metadata (JSON) into WASM linear memory via `alloc`. Plugin reads inputs, runs logic, writes result to its own memory. Returns `0` (no violation) or `1` (violation found).

**`alloc`**: Plugin-side memory allocator. Host calls this to get a pointer where it can write input data. Plugin must return a valid pointer to `size` bytes of writable memory.

**`get_result_ptr` / `get_result_len`**: After `check` returns `1`, host calls these to read the violation result from WASM memory.

### Metadata JSON (passed to plugin)

```json
{
  "tenant_id": "default",
  "model": "gpt-4o",
  "provider": "openai",
  "phase": "input"
}
```

All five fields are always present.

### Result JSON (returned by plugin on violation)

```json
{
  "action": "block",
  "message": "toxicity score 0.92 exceeds threshold"
}
```

**Config wins over plugin action.** The `action` field in the result is informational — the gateway enforces whatever `action` is set in `aegisflow.yaml`. This prevents a plugin from escalating its own permissions.

## Config Integration

```yaml
policies:
  input:
    - name: "custom-toxicity"
      type: "wasm"
      action: "block"
      path: "plugins/toxicity.wasm"
      timeout: 100ms
      on_error: "block"

    - name: "custom-pii-scanner"
      type: "wasm"
      action: "warn"
      path: "plugins/pii.wasm"
      timeout: 200ms
      on_error: "allow"
```

### Config fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | required | Policy name (used in logs/violations) |
| `type` | string | required | Must be `"wasm"` |
| `action` | string | required | `"block"`, `"warn"`, or `"log"` — overrides plugin's returned action |
| `path` | string | required | Relative or absolute path to `.wasm` file |
| `timeout` | duration | `100ms` | Max execution time per `check` call |
| `on_error` | string | `"block"` | `"block"` or `"allow"` — behavior when plugin crashes/times out |

### Changes to `PolicyConfig` struct

Add three fields:

```go
type PolicyConfig struct {
    Name     string        `yaml:"name"`
    Type     string        `yaml:"type"`
    Action   string        `yaml:"action"`
    Keywords []string      `yaml:"keywords"`
    Patterns []string      `yaml:"patterns"`
    Path     string        `yaml:"path"`      // NEW: .wasm file path
    Timeout  time.Duration `yaml:"timeout"`   // NEW: execution timeout
    OnError  string        `yaml:"on_error"`  // NEW: "block" or "allow"
}
```

## Loading Lifecycle

1. **Startup** — gateway loads `.wasm` files, wazero compiles modules (compilation is cached)
2. **Hot-reload** — config watcher detects new/changed/removed WASM plugins, reloads affected modules
3. **Shutdown** — closes wazero runtime, frees resources

## Execution Limits

| Limit | Value | Rationale |
|-------|-------|-----------|
| Timeout | Configurable, default 100ms | Policy checks must be fast — they're in the request path |
| Memory | 16MB per module | Sufficient for policy logic, prevents runaway allocation |

## Error Handling

| Error | Behavior |
|-------|----------|
| `.wasm` file not found | Log error at startup, skip filter (don't crash gateway) |
| Plugin missing required exports (`check`, `alloc`, `get_result_ptr`, `get_result_len`) | Reject plugin at load time with clear error message |
| Plugin panics during `check` | Catch via wazero, apply `on_error` policy |
| Plugin exceeds timeout | Cancel execution context, apply `on_error` policy |
| Plugin returns invalid result JSON | Treat as error, apply `on_error` policy |

When `on_error: "block"`, the violation message is: `"wasm plugin error: <plugin_name>: <error_detail>"`.
When `on_error: "allow"`, the error is logged but the request proceeds.

## Testing Strategy

### Unit tests (`filter_wasm_test.go`)

- Successful block (plugin returns 1 with valid result)
- Successful allow (plugin returns 0)
- Timeout enforcement (slow plugin gets cancelled)
- Panic recovery (plugin crashes, `on_error` applied)
- Invalid result JSON (malformed response, `on_error` applied)
- Missing exports (plugin rejected at load time)

### Integration test

- Wire a WASM filter into the policy engine alongside a built-in keyword filter
- Verify both run in sequence, WASM filter participates in the pipeline

### Test binary

A small TinyGo `.wasm` binary compiled as part of `go generate` or checked into the repo for CI.

## Example Plugin

Located at `examples/wasm-plugin/`:

```
examples/wasm-plugin/
  main.go       # TinyGo source — blocks messages containing "forbidden"
  Makefile       # tinygo build -o plugin.wasm -target wasi ./main.go
  README.md      # ABI docs, build instructions, config example
```

The example demonstrates:
- Implementing `alloc`, `check`, `get_result_ptr`, `get_result_len`
- Reading content and metadata from host memory
- Returning a violation result

## Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `github.com/tetratelabs/wazero` | latest | Pure Go WASM runtime, no CGO |

No other new dependencies.
