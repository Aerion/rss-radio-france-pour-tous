package config

import (
	"testing"
	"time"
)

func TestDurationEnv(t *testing.T) {
	t.Setenv("TEST_DURATION", "")
	got, err := durationEnv("TEST_DURATION", 5*time.Minute)
	if err != nil {
		t.Fatalf("durationEnv: %v", err)
	}
	if got != 5*time.Minute {
		t.Errorf("got %v, want the default 5m", got)
	}

	t.Setenv("TEST_DURATION", "90s")
	got, err = durationEnv("TEST_DURATION", 5*time.Minute)
	if err != nil {
		t.Fatalf("durationEnv: %v", err)
	}
	if got != 90*time.Second {
		t.Errorf("got %v, want 90s", got)
	}

	t.Setenv("TEST_DURATION", "not-a-duration")
	if _, err := durationEnv("TEST_DURATION", 5*time.Minute); err == nil {
		t.Error("expected an error for an invalid duration")
	}
}

func TestIntEnv(t *testing.T) {
	t.Setenv("TEST_INT", "")
	got, err := intEnv("TEST_INT", 42)
	if err != nil {
		t.Fatalf("intEnv: %v", err)
	}
	if got != 42 {
		t.Errorf("got %d, want the default 42", got)
	}

	t.Setenv("TEST_INT", "7")
	got, err = intEnv("TEST_INT", 42)
	if err != nil {
		t.Fatalf("intEnv: %v", err)
	}
	if got != 7 {
		t.Errorf("got %d, want 7", got)
	}

	t.Setenv("TEST_INT", "not-an-int")
	if _, err := intEnv("TEST_INT", 42); err == nil {
		t.Error("expected an error for an invalid integer")
	}
}
