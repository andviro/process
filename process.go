package process

import (
	"gopkg.in/andviro/go-state.v2"

	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

const (
	restartTimeout = 100
	startTimeout   = 1000
	backoffTimeout = 5000
	stopTimeout    = 20000
	killTimeout    = 5000
)

// Process presents basic execution unit
type Process struct {
	// Initial configuration
	Cmd              string    `json:"cmd"`              // A path to executable to run
	Args             []string  `json:"args"`             // Command-line argument list
	Dir              string    `json:"dir"`              // Process working directory
	Env              []string  `json:"env"`              // Inital environment
	Stdout, Stderr   io.Writer `json:"-"`                // Standard IO pipes
	StartTimeout     int       `json:"startTimeout"`     // Time to wait for process start in milliseconds
	BackoffTimeout   int       `json:"backoffTimeout"`   // Delay before another start attempt
	StopTimeout      int       `json:"stopTimeout"`      // Time to wait for process stop in milliseconds
	KillTimeout      int       `json:"killTimeout"`      // Time to wait after sending the kill signal in milliseconds
	MaxStartAttempts int       `json:"maxStartAttempts"` // Maximum number of start attempts
	MaxRestarts      int       `json:"maxRestarts"`      // Maximum number of restarts
	RestartTimeout   int       `json:"restartTimeout"`   // Delay before restart attempt
	RestartPolicy    string    `json:"restartPolicy"`    // One of: "always", "on-error", ""

	// Process run-time parameters
	StartAttempt int    `json:"startAttempt"` // Current number of start attempts
	RestartCount int    `json:"restartCount"` // Current number of runs
	State        string `json:"state"`        // Current process state
	LastError    error  `json:"lastError"`    // Last error encountered

	Stop   context.CancelFunc
	cmd    *exec.Cmd
	result chan error
}

func (p *Process) logf(format string, args ...interface{}) (n int, err error) {
	if p.Stderr == nil {
		return
	}
	return fmt.Fprintf(p.Stderr, format, args...)
}

// Run starts process execution
func (p *Process) Run(ctx context.Context) (res chan error) {
	res = make(chan error, 1)
	ctx, p.Stop = context.WithCancel(ctx)

	if p.StartTimeout == 0 {
		p.StartTimeout = startTimeout
	}
	if p.StopTimeout == 0 {
		p.StopTimeout = stopTimeout
	}
	if p.BackoffTimeout == 0 {
		p.BackoffTimeout = backoffTimeout
	}
	if p.RestartTimeout == 0 {
		p.RestartTimeout = restartTimeout
	}
	if p.KillTimeout == 0 {
		p.KillTimeout = killTimeout
	}
	go func() {
		defer close(res)
		res <- state.Run(ctx, p.starting, func(ctx context.Context) error {
			p.State = state.Name(ctx)
			return nil
		})
	}()
	return
}

func (p *Process) starting(c context.Context) (res state.Func) {
	p.logf("%v starting %s", time.Now(), p.Cmd)

	p.cmd = exec.Command(p.Cmd, p.Args...)
	p.cmd.Dir = p.Dir
	p.cmd.Env = p.Env
	p.cmd.Stdout = p.Stdout
	p.cmd.Stderr = p.Stderr

	if p.LastError = p.cmd.Start(); p.LastError != nil {
		p.logf("%v error starting %s: %v", time.Now(), p.Cmd, p.LastError)
		return p.failed
	}
	p.result = make(chan error, 1)
	go func() {
		defer close(p.result)
		p.result <- p.cmd.Wait()
	}()

	select {
	case <-c.Done():
		return p.stopping
	case p.LastError = <-p.result:
		p.logf("%v %s finished with error: %v", time.Now(), p.Cmd, p.LastError)
		switch p.RestartPolicy {
		case "on-failure":
			if p.LastError == nil {
				break
			}
			fallthrough
		case "always":
			return p.backoff
		}
		return p.stopped
	case <-time.After(time.Duration(p.StartTimeout) * time.Millisecond):
		p.LastError = nil
		p.StartAttempt = 0
	}
	return p.running
}

func (p *Process) stopping(c context.Context) (res state.Func) {
	if p.LastError = p.cmd.Process.Signal(os.Interrupt); p.LastError != nil {
		return p.failed
	}
	select {
	case p.LastError = <-p.result:
		break
	case <-time.After(time.Duration(p.StopTimeout) * time.Millisecond):
		return p.killing
	}
	return p.stopped
}

func (p *Process) killing(c context.Context) (res state.Func) {
	if p.LastError = p.cmd.Process.Signal(os.Kill); p.LastError != nil {
		return p.failed
	}
	select {
	case p.LastError = <-p.result:
		break
	case <-time.After(time.Duration(p.KillTimeout) * time.Millisecond):
		p.LastError = fmt.Errorf("failed to kill process")
		return p.failed
	}
	return p.stopped
}

func (p *Process) backoff(c context.Context) (res state.Func) {
	p.StartAttempt++
	if p.MaxStartAttempts != 0 && p.StartAttempt >= p.MaxStartAttempts {
		p.LastError = fmt.Errorf("maximum start attempts reached (last error: %v)", p.LastError)
		return p.failed
	}
	select {
	case <-c.Done():
		return p.stopping
	case <-time.After(time.Duration(p.BackoffTimeout) * time.Millisecond):
		p.LastError = nil
	}
	return p.starting
}

func (p *Process) failed(c context.Context) (res state.Func) {
	return
}

func (p *Process) restarting(c context.Context) (res state.Func) {
	p.RestartCount++
	if p.MaxRestarts != 0 && p.RestartCount >= p.MaxRestarts {
		if p.LastError != nil {
			return p.failed
		}
		return p.stopped
	}
	select {
	case <-c.Done():
		return p.stopping
	case <-time.After(time.Duration(p.RestartTimeout) * time.Millisecond):
		p.LastError = nil
	}
	return p.starting
}

func (p *Process) running(c context.Context) (res state.Func) {
	select {
	case <-c.Done():
		p.logf("%v %s received cancel signal", time.Now(), p.Cmd)
		return p.stopping
	case p.LastError = <-p.result:
		p.logf("%v %s finished with error: %v", time.Now(), p.Cmd, p.LastError)
		switch p.RestartPolicy {
		case "on-failure":
			if p.LastError == nil {
				break
			}
			fallthrough
		case "always":
			return p.restarting
		}
		return p.stopped
	}
}

func (p *Process) stopped(c context.Context) (res state.Func) {
	return
}
