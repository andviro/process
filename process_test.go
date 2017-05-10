package process_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/andviro/process"
)

func TestRun(t *testing.T) {
	p := &process.Process{
		Cmd:  "/bin/sleep",
		Args: []string{"1"},
	}
	if err := <-p.Run(context.TODO()); err != nil {
		t.Fatalf("%+v", err)
	}
	if p.LastError != nil {
		t.Errorf("%+v", p.LastError)
	}
	if p.State != "stopped" {
		t.Errorf("invalid final state: %s", p.State)
	}
}

func TestRestart(t *testing.T) {
	p := &process.Process{
		Cmd:           "/bin/sleep",
		Args:          []string{"1"},
		RestartPolicy: "always",
		MaxRestarts:   3,
	}

	res := p.Run(context.TODO())
	select {
	case err := <-res:
		if err != nil {
			t.Fatalf("%+v", err)
		}
		if p.LastError != nil {
			t.Errorf("%#v", p.LastError)
		}
		if p.RestartCount != 3 {
			t.Errorf("invalid restart count %d", p.RestartCount)
		}
	case <-time.After(5000 * time.Millisecond):
		t.Fatal("process not stopped", p)
	}
}

func TestStop(t *testing.T) {
	p := &process.Process{
		Cmd:  "/bin/sleep",
		Args: []string{"3"},
	}
	res := p.Run(context.TODO())
	go func() {
		defer p.Stop()
		time.Sleep(1 * time.Second)
	}()

	select {
	case err := <-res:
		if err != nil {
			t.Fatalf("%+v", err)
		}
		_, ok := p.LastError.(*exec.ExitError)
		if !ok {
			t.Errorf("%#v", p.LastError)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("process not stopped")
	}
}

func TestCancelContext(t *testing.T) {
	p := &process.Process{
		Cmd:  "/bin/sleep",
		Args: []string{"3"},
	}
	ctx, cancel := context.WithCancel(context.TODO())
	go func() {
		defer cancel()
		time.Sleep(1 * time.Second)
	}()

	select {
	case err := <-p.Run(ctx):
		if err != nil {
			t.Fatalf("%+v", err)
		}
		_, ok := p.LastError.(*exec.ExitError)
		if !ok {
			t.Errorf("%#v", p.LastError)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("process not stopped")
	}
}
