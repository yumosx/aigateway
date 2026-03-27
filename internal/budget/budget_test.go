package budget

import "testing"

func TestCheckAllowedUnderBudget(t *testing.T) {
	mgr := NewManager([]SpendScope{
		{Scope: "global", ScopeID: "global", Limit: 100, AlertAt: 80, WarnAt: 90},
	})
	mgr.RecordSpend("t1", "gpt-4o", 50)
	result := mgr.Check("t1", "gpt-4o")
	if !result.Allowed {
		t.Error("expected allowed under budget")
	}
	if len(result.Warnings) > 0 {
		t.Error("expected no warnings under budget")
	}
}

func TestCheckBlockedOverBudget(t *testing.T) {
	mgr := NewManager([]SpendScope{
		{Scope: "global", ScopeID: "global", Limit: 100, AlertAt: 80, WarnAt: 90},
	})
	mgr.RecordSpend("t1", "gpt-4o", 101)
	result := mgr.Check("t1", "gpt-4o")
	if result.Allowed {
		t.Error("expected blocked over budget")
	}
	if result.BlockMsg == "" {
		t.Error("expected BlockMsg when over budget")
	}
}

func TestCheckWarningAtThreshold(t *testing.T) {
	mgr := NewManager([]SpendScope{
		{Scope: "global", ScopeID: "global", Limit: 100, AlertAt: 80, WarnAt: 90},
	})
	mgr.RecordSpend("t1", "gpt-4o", 91)
	result := mgr.Check("t1", "gpt-4o")
	if !result.Allowed {
		t.Error("expected allowed at warn threshold")
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warning at 91% with WarnAt=90")
	}
}

func TestCheckAlertAtThreshold(t *testing.T) {
	mgr := NewManager([]SpendScope{
		{Scope: "global", ScopeID: "global", Limit: 100, AlertAt: 80, WarnAt: 90},
	})
	mgr.RecordSpend("t1", "gpt-4o", 81)
	statuses := mgr.AllStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected at least one status")
	}
	found := false
	for _, s := range statuses {
		if s.ScopeID == "global" {
			found = true
			if s.Status != "alert" {
				t.Errorf("expected status 'alert' at 81%%, got %q", s.Status)
			}
		}
	}
	if !found {
		t.Error("expected global scope in statuses")
	}
}

func TestRecordSpendMultipleScopes(t *testing.T) {
	mgr := NewManager([]SpendScope{
		{Scope: "global", ScopeID: "global", Limit: 200, AlertAt: 80, WarnAt: 90},
		{Scope: "tenant", ScopeID: "premium", Limit: 100, AlertAt: 80, WarnAt: 90},
	})
	mgr.RecordSpend("premium", "gpt-4o", 30)

	statuses := mgr.AllStatuses()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}
	for _, s := range statuses {
		if s.CurrentSpend != 30 {
			t.Errorf("scope %s: expected spend 30, got %.2f", s.ScopeID, s.CurrentSpend)
		}
	}
}

func TestCheckTenantBudget(t *testing.T) {
	mgr := NewManager([]SpendScope{
		{Scope: "tenant", ScopeID: "premium", Limit: 50, AlertAt: 80, WarnAt: 90},
	})
	mgr.RecordSpend("premium", "gpt-4o", 51)
	result := mgr.Check("premium", "gpt-4o")
	if result.Allowed {
		t.Error("expected blocked for tenant over budget")
	}
	if result.BlockMsg == "" {
		t.Error("expected BlockMsg for tenant over budget")
	}
}

func TestCheckModelBudget(t *testing.T) {
	mgr := NewManager([]SpendScope{
		{Scope: "tenant_model", ScopeID: "premium:gpt-4o", Limit: 20, AlertAt: 80, WarnAt: 90},
	})
	mgr.RecordSpend("premium", "gpt-4o", 21)
	result := mgr.Check("premium", "gpt-4o")
	if result.Allowed {
		t.Error("expected blocked for tenant_model over budget")
	}
	if result.BlockMsg == "" {
		t.Error("expected BlockMsg for tenant_model over budget")
	}
}

func TestAllStatuses(t *testing.T) {
	mgr := NewManager([]SpendScope{
		{Scope: "global", ScopeID: "global", Limit: 100, AlertAt: 80, WarnAt: 90},
		{Scope: "tenant", ScopeID: "premium", Limit: 50, AlertAt: 80, WarnAt: 90},
		{Scope: "tenant_model", ScopeID: "premium:gpt-4o", Limit: 20, AlertAt: 80, WarnAt: 90},
	})
	mgr.RecordSpend("premium", "gpt-4o", 10)

	statuses := mgr.AllStatuses()
	if len(statuses) != 3 {
		t.Fatalf("expected 3 statuses, got %d", len(statuses))
	}

	expected := map[string]float64{
		"global":         10.0,
		"premium":        20.0,
		"premium:gpt-4o": 50.0,
	}
	for _, s := range statuses {
		wantPct, ok := expected[s.ScopeID]
		if !ok {
			t.Errorf("unexpected scopeID %q", s.ScopeID)
			continue
		}
		if s.Percentage != wantPct {
			t.Errorf("scope %s: expected percentage %.1f, got %.1f", s.ScopeID, wantPct, s.Percentage)
		}
	}
}

func TestForecastProjection(t *testing.T) {
	mgr := NewManager([]SpendScope{
		{Scope: "global", ScopeID: "global", Limit: 100, AlertAt: 80, WarnAt: 90},
	})
	mgr.RecordSpend("t1", "gpt-4o", 10)

	fc := mgr.Forecast("global", 100)
	if fc.ScopeID != "global" {
		t.Errorf("expected ScopeID 'global', got %q", fc.ScopeID)
	}
	if fc.ProjectedTotal <= 0 {
		t.Error("expected positive projected total")
	}
	if fc.DaysRemaining < 0 {
		t.Error("expected non-negative days remaining")
	}
}

func TestCheckNoLimitUnlimited(t *testing.T) {
	mgr := NewManager([]SpendScope{
		{Scope: "global", ScopeID: "global", Limit: 0, AlertAt: 80, WarnAt: 90},
	})
	mgr.RecordSpend("t1", "gpt-4o", 999999)
	result := mgr.Check("t1", "gpt-4o")
	if !result.Allowed {
		t.Error("expected allowed with zero limit (unlimited)")
	}
	if len(result.Warnings) > 0 {
		t.Error("expected no warnings with zero limit")
	}
	if result.BlockMsg != "" {
		t.Error("expected no block message with zero limit")
	}
}
