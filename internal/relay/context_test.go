package relay

import (
	"testing"
)

func TestExecutionContext_GetSet(t *testing.T) {
	ec := NewExecutionContext("notx:device:test", map[string]string{"a": "1"})

	v, ok := ec.Get("a")
	if !ok || v != "1" {
		t.Errorf("expected a=1, got ok=%v v=%q", ok, v)
	}

	ec.Set("b", "2")
	v2, ok2 := ec.Get("b")
	if !ok2 || v2 != "2" {
		t.Errorf("expected b=2, got ok=%v v=%q", ok2, v2)
	}
}

func TestExecutionContext_UnknownKey(t *testing.T) {
	ec := NewExecutionContext("notx:device:test", nil)
	_, ok := ec.Get("missing")
	if ok {
		t.Error("expected ok=false for missing key")
	}
}

func TestExecutionContext_SetAll(t *testing.T) {
	ec := NewExecutionContext("notx:device:test", nil)
	ec.SetAll(map[string]string{"x": "10", "y": "20"})

	if v, _ := ec.Get("x"); v != "10" {
		t.Errorf("expected x=10, got %q", v)
	}
	if v, _ := ec.Get("y"); v != "20" {
		t.Errorf("expected y=20, got %q", v)
	}
}

func TestExecutionContext_Snapshot_IsCopy(t *testing.T) {
	ec := NewExecutionContext("notx:device:test", map[string]string{"a": "orig"})
	snap := ec.Snapshot()
	snap["a"] = "mutated"

	// The original must not be affected.
	if v, _ := ec.Get("a"); v != "orig" {
		t.Errorf("snapshot mutation leaked back into context: %q", v)
	}
}

func TestExecutionContext_DeviceURN(t *testing.T) {
	ec := NewExecutionContext("notx:device:abc-123", nil)
	if ec.DeviceURN != "notx:device:abc-123" {
		t.Errorf("expected device URN to be set, got %q", ec.DeviceURN)
	}
}
