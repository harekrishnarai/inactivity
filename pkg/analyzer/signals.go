package analyzer

import (
	"context"
	"errors"
	"os"
	"os/signal"
)

type StopDecision int

const (
	ContinueRunning StopDecision = iota
	StopGracefully
	StopImmediately
)

type SignalController struct {
	interrupts int
}

var ErrGracefulStop = errors.New("graceful stop requested")

func NewSignalController() *SignalController {
	return &SignalController{}
}

func (s *SignalController) HandleInterrupt() StopDecision {
	s.interrupts++
	if s.interrupts == 1 {
		return StopGracefully
	}
	return StopImmediately
}

func watchInterrupts(cancel context.CancelFunc) func() {
	controller := NewSignalController()
	signals := make(chan os.Signal, 2)
	done := make(chan struct{})

	signal.Notify(signals, os.Interrupt)

	go func() {
		for {
			select {
			case <-done:
				return
			case <-signals:
				if controller.HandleInterrupt() == StopImmediately {
					os.Exit(1)
				}
				cancel()
			}
		}
	}()

	return func() {
		signal.Stop(signals)
		close(done)
	}
}
