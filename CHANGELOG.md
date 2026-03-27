# Changelog

All notable changes to AegisFlow will be documented in this file.

## [0.2.0] - 2026-03-26

### Added
- WASM policy plugin support via wazero runtime
- Custom policy filters in any WASM-compatible language (Go, Rust, TinyGo, AssemblyScript)
- Configurable per-plugin timeout and error handling (on_error: block/allow)
- Example WASM plugin with ABI documentation

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
