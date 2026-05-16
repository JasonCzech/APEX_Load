package protocol

import (
	"context"
	"testing"
)

type mockProtocol struct{ name string }

func (m *mockProtocol) Name() string                                   { return m.name }
func (m *mockProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (m *mockProtocol) Execute(_ context.Context, _ *Action) (*Result, error) {
	return &Result{Success: true}, nil
}
func (m *mockProtocol) Teardown(_ context.Context) error    { return nil }
func (m *mockProtocol) HealthCheck(_ context.Context) error { return nil }

func TestRegisterAndGet(t *testing.T) {
	Register("test-proto", func() Protocol {
		return &mockProtocol{name: "test-proto"}
	})

	p, err := Get("test-proto")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "test-proto" {
		t.Errorf("name = %q, want %q", p.Name(), "test-proto")
	}
}

func TestGet_Unknown(t *testing.T) {
	_, err := Get("nonexistent-protocol")
	if err == nil {
		t.Fatal("expected error for unknown protocol")
	}
}

func TestRegister_Duplicate(t *testing.T) {
	Register("dup-test", func() Protocol {
		return &mockProtocol{name: "dup-test"}
	})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()

	Register("dup-test", func() Protocol {
		return &mockProtocol{name: "dup-test"}
	})
}

func TestList(t *testing.T) {
	Register("list-test-a", func() Protocol { return &mockProtocol{} })
	Register("list-test-b", func() Protocol { return &mockProtocol{} })

	names := List()
	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}

	if !found["list-test-a"] || !found["list-test-b"] {
		t.Errorf("List() missing registered protocols, got %v", names)
	}
}

func TestGet_ReturnsNewInstance(t *testing.T) {
	Register("instance-test", func() Protocol {
		return &mockProtocol{name: "instance-test"}
	})

	p1, _ := Get("instance-test")
	p2, _ := Get("instance-test")

	if p1 == p2 {
		t.Error("Get() should return new instances, got same pointer")
	}
}
