# Changelog

All notable changes to AegisFlow will be documented in this file.

## [0.2.1] - 2026-03-27

### Added
- Cache stats dashboard page with hit/miss counters, hit rate, eviction tracking, and live chart
- Policy violations dashboard page with violation history, per-policy and per-tenant breakdowns
- Cache stats API endpoint (`/admin/v1/cache`)
- Policy violations API endpoint (`/admin/v1/violations`)

### Fixed
- Token rate limiter now reads actual request body size instead of trusting Content-Length header
- Token rate limiter fails closed on error (was failing open)
- Replaced unbounded goroutine DB writes with buffered channel worker (prevents memory leak)

### Changed
- Redis and PostgreSQL ports are now internal-only in Docker Compose (no longer exposed to host)
- Added health check for aegisflow service in Docker Compose
- Added RealIP trusted proxy documentation in gateway setup

## [0.2.0] - 2026-03-26

### Added
- WASM policy plugin support via wazero runtime
- Custom policy filters in any WASM-compatible language (Go, Rust, TinyGo, AssemblyScript)
- Configurable per-plugin timeout and error handling (on_error: block/allow)
- Example WASM plugin with ABI documentation
- Live request feed dashboard page

### Security
- Timing-safe tenant API key comparison (SHA-256 + subtle.ConstantTimeCompare)
- Admin endpoints blocked by default when token is unconfigured
- Removed admin token from URL query params (header-only auth)
- 10MB request body size limit
- Cache keys scoped by tenant ID (cross-tenant data leak fix)
- SSE injection fix via json.Marshal
- Rate limiter fails closed (503) instead of open
- NFKC Unicode normalization for keyword policy filter
- Expanded jailbreak keyword list (3 to 25 patterns)

## [0.1.0] - 2024-03-24

### Added
- Unified AI gateway with OpenAI-compatible API
- Provider adapters: Mock, OpenAI, Anthropic, Ollama
- Intelligent routing with glob-based model matching
- Priority and round-robin routing strategies
- Automatic fallback with circuit breaker
- In-memory rate limiting (sliding window)
- Redis-backed rate limiting (optional)
- Tenant authentication via API keys
- Policy engine with input and output checks
- Keyword blocklist filter
- Regex pattern filter
- PII detection (email, SSN, credit card)
- Usage tracking with per-tenant, per-model aggregation
- Cost estimation per request
- OpenTelemetry tracing (stdout and OTLP exporters)
- Prometheus metrics endpoint
- Structured JSON logging
- Admin API with health, metrics, and usage endpoints
- Docker and Docker Compose support
- GitHub Actions CI/CD
- Comprehensive test suite
