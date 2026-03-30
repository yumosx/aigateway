package config

import (
	"os"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	yaml := `
server:
  port: 9090
  admin_port: 9091
providers:
  - name: "mock"
    type: "mock"
    enabled: true
    default: true
routes:
  - match:
      model: "*"
    providers: ["mock"]
    strategy: "priority"
tenants:
  - id: "test"
    name: "Test Tenant"
    api_keys:
      - "test-key"
    rate_limit:
      requests_per_minute: 10
      tokens_per_minute: 1000
    allowed_models:
      - "*"
`
	f, err := os.CreateTemp("", "aegisflow-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(yaml); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Server.AdminPort != 9091 {
		t.Errorf("expected admin port 9091, got %d", cfg.Server.AdminPort)
	}
	if len(cfg.Providers) != 1 {
		t.Errorf("expected 1 provider, got %d", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "mock" {
		t.Errorf("expected provider name 'mock', got '%s'", cfg.Providers[0].Name)
	}
	if len(cfg.Tenants) != 1 {
		t.Errorf("expected 1 tenant, got %d", len(cfg.Tenants))
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("expected default host '0.0.0.0', got '%s'", cfg.Server.Host)
	}
}

func TestFindTenantByAPIKey(t *testing.T) {
	cfg := &Config{
		Tenants: []TenantConfig{
			{ID: "t1", APIKeys: []APIKeyEntry{{Key: "key-a", Role: "operator"}, {Key: "key-b", Role: "operator"}}},
			{ID: "t2", APIKeys: []APIKeyEntry{{Key: "key-c", Role: "operator"}}},
		},
	}

	match := cfg.FindTenantByAPIKey("key-b")
	if match == nil || match.Tenant.ID != "t1" {
		t.Errorf("expected tenant t1, got %v", match)
	}

	match = cfg.FindTenantByAPIKey("key-c")
	if match == nil || match.Tenant.ID != "t2" {
		t.Errorf("expected tenant t2, got %v", match)
	}

	match = cfg.FindTenantByAPIKey("nonexistent")
	if match != nil {
		t.Errorf("expected nil for nonexistent key, got %v", match)
	}
}

func TestAPIKeyEntryNewFormat(t *testing.T) {
	yamlData := `
tenants:
  - id: "t1"
    name: "Tenant 1"
    api_keys:
      - key: "admin-key"
        role: "admin"
      - key: "viewer-key"
        role: "viewer"
`
	f, err := os.CreateTemp("", "aegisflow-newformat-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(yamlData)
	f.Close()

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if len(cfg.Tenants) != 1 {
		t.Fatalf("expected 1 tenant, got %d", len(cfg.Tenants))
	}
	keys := cfg.Tenants[0].APIKeys
	if len(keys) != 2 {
		t.Fatalf("expected 2 api keys, got %d", len(keys))
	}
	if keys[0].Key != "admin-key" || keys[0].Role != "admin" {
		t.Errorf("expected {admin-key, admin}, got {%s, %s}", keys[0].Key, keys[0].Role)
	}
	if keys[1].Key != "viewer-key" || keys[1].Role != "viewer" {
		t.Errorf("expected {viewer-key, viewer}, got {%s, %s}", keys[1].Key, keys[1].Role)
	}
}

func TestAPIKeyEntryOldFormat(t *testing.T) {
	yamlData := `
tenants:
  - id: "t1"
    name: "Tenant 1"
    api_keys:
      - "plain-key-1"
      - "plain-key-2"
`
	f, err := os.CreateTemp("", "aegisflow-oldformat-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(yamlData)
	f.Close()

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	keys := cfg.Tenants[0].APIKeys
	if len(keys) != 2 {
		t.Fatalf("expected 2 api keys, got %d", len(keys))
	}
	if keys[0].Key != "plain-key-1" || keys[0].Role != "operator" {
		t.Errorf("expected {plain-key-1, operator}, got {%s, %s}", keys[0].Key, keys[0].Role)
	}
	if keys[1].Key != "plain-key-2" || keys[1].Role != "operator" {
		t.Errorf("expected {plain-key-2, operator}, got {%s, %s}", keys[1].Key, keys[1].Role)
	}
}

func TestAPIKeyEntryMixedFormat(t *testing.T) {
	yamlData := `
tenants:
  - id: "t1"
    name: "Tenant 1"
    api_keys:
      - "plain-key"
      - key: "structured-key"
        role: "admin"
`
	f, err := os.CreateTemp("", "aegisflow-mixed-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(yamlData)
	f.Close()

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	keys := cfg.Tenants[0].APIKeys
	if len(keys) != 2 {
		t.Fatalf("expected 2 api keys, got %d", len(keys))
	}
	if keys[0].Key != "plain-key" || keys[0].Role != "operator" {
		t.Errorf("expected {plain-key, operator}, got {%s, %s}", keys[0].Key, keys[0].Role)
	}
	if keys[1].Key != "structured-key" || keys[1].Role != "admin" {
		t.Errorf("expected {structured-key, admin}, got {%s, %s}", keys[1].Key, keys[1].Role)
	}
}

func TestAPIKeyEntryDefaultRole(t *testing.T) {
	yamlData := `
tenants:
  - id: "t1"
    name: "Tenant 1"
    api_keys:
      - key: "no-role-key"
`
	f, err := os.CreateTemp("", "aegisflow-defaultrole-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(yamlData)
	f.Close()

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	keys := cfg.Tenants[0].APIKeys
	if len(keys) != 1 {
		t.Fatalf("expected 1 api key, got %d", len(keys))
	}
	if keys[0].Key != "no-role-key" || keys[0].Role != "operator" {
		t.Errorf("expected {no-role-key, operator}, got {%s, %s}", keys[0].Key, keys[0].Role)
	}
}

func TestFindTenantByAPIKeyReturnsRole(t *testing.T) {
	cfg := &Config{
		Tenants: []TenantConfig{
			{
				ID: "t1",
				APIKeys: []APIKeyEntry{
					{Key: "op-key", Role: "operator"},
					{Key: "admin-key", Role: "admin"},
				},
			},
		},
	}

	match := cfg.FindTenantByAPIKey("op-key")
	if match == nil {
		t.Fatal("expected match, got nil")
	}
	if match.Role != "operator" {
		t.Errorf("expected role operator, got %s", match.Role)
	}

	match = cfg.FindTenantByAPIKey("admin-key")
	if match == nil {
		t.Fatal("expected match, got nil")
	}
	if match.Role != "admin" {
		t.Errorf("expected role admin, got %s", match.Role)
	}
	if match.Tenant.ID != "t1" {
		t.Errorf("expected tenant t1, got %s", match.Tenant.ID)
	}
}
