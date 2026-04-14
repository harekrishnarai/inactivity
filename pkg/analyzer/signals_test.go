package analyzer

import "testing"

func TestSignalControllerEscalatesOnSecondInterrupt(t *testing.T) {
	controller := NewSignalController()

	first := controller.HandleInterrupt()
	second := controller.HandleInterrupt()

	if first != StopGracefully {
		t.Fatalf("expected graceful stop on first interrupt, got %v", first)
	}
	if second != StopImmediately {
		t.Fatalf("expected immediate stop on second interrupt, got %v", second)
	}
}
