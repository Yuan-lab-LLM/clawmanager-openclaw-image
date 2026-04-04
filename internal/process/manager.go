package process

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	appconfig "github.com/iamlovingit/clawmanager-openclaw-image/internal/config"
)

type Status string

const (
	StatusStarting    Status = "starting"
	StatusRunning     Status = "running"
	StatusStopped     Status = "stopped"
	StatusStopping    Status = "stopping"
	StatusCrashed     Status = "crashed"
	StatusConfiguring Status = "configuring"
	StatusUnknown     Status = "unknown"
)

type Snapshot struct {
	Status              Status        `json:"status"`
	PID                 int           `json:"pid,omitempty"`
	StartedAt           time.Time     `json:"started_at,omitempty"`
	ExitedAt            time.Time     `json:"exited_at,omitempty"`
	LastExitCode        int           `json:"last_exit_code,omitempty"`
	LastExitReason      string        `json:"last_exit_reason,omitempty"`
	LastOperation       string        `json:"last_operation,omitempty"`
	LastOperationResult string        `json:"last_operation_result,omitempty"`
	Restarts            int           `json:"restarts"`
	Uptime              time.Duration `json:"uptime,omitempty"`
}

type Manager struct {
	cfg appconfig.Config

	mu                  sync.RWMutex
	cmd                 *exec.Cmd
	status              Status
	startedAt           time.Time
	exitedAt            time.Time
	lastExitCode        int
	lastExitReason      string
	lastOperation       string
	lastOperationResult string
	restarts            int
	done                chan struct{}
	stopRequested       bool
}

func New(cfg appconfig.Config) *Manager {
	return &Manager{cfg: cfg, status: StatusStopped}
}

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil && m.status != StatusStopped && m.status != StatusCrashed {
		m.lastOperation = "start_openclaw"
		m.lastOperationResult = "noop_already_running"
		return nil
	}
	if len(m.cfg.OpenClawCommand) == 0 {
		return errors.New("openclaw command is empty")
	}

	cmd := exec.CommandContext(context.Background(), m.cfg.OpenClawCommand[0], m.cfg.OpenClawCommand[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	m.status = StatusStarting
	m.lastOperation = "start_openclaw"
	if err := cmd.Start(); err != nil {
		m.status = StatusCrashed
		m.lastOperationResult = err.Error()
		m.lastExitReason = err.Error()
		return fmt.Errorf("start openclaw: %w", err)
	}

	m.cmd = cmd
	m.done = make(chan struct{})
	m.stopRequested = false
	m.startedAt = time.Now().UTC()
	m.exitedAt = time.Time{}
	m.lastExitReason = ""
	m.lastOperationResult = "started"

	go m.wait(cmd)
	go m.promoteRunning(cmd.Process.Pid)
	return nil
}

func (m *Manager) promoteRunning(pid int) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case <-timeout.C:
			m.mu.Lock()
			if m.cmd != nil && m.cmd.Process != nil && m.cmd.Process.Pid == pid && m.status == StatusStarting {
				m.status = StatusRunning
				m.lastOperationResult = "running"
			}
			m.mu.Unlock()
			return
		case <-ticker.C:
			if m.checkHealth() {
				m.mu.Lock()
				if m.cmd != nil && m.cmd.Process != nil && m.cmd.Process.Pid == pid && m.status == StatusStarting {
					m.status = StatusRunning
					m.lastOperationResult = "running"
				}
				m.mu.Unlock()
				return
			}
		}
	}
}

func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	cmd := m.cmd
	done := m.done
	if cmd == nil || cmd.Process == nil || m.status == StatusStopped {
		m.lastOperation = "stop_openclaw"
		m.lastOperationResult = "noop_already_stopped"
		m.status = StatusStopped
		m.mu.Unlock()
		return nil
	}
	m.status = StatusStopping
	m.lastOperation = "stop_openclaw"
	m.stopRequested = true
	m.mu.Unlock()

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		m.mu.Lock()
		m.lastOperationResult = err.Error()
		m.mu.Unlock()
		return fmt.Errorf("signal openclaw: %w", err)
	}

	timer := time.NewTimer(m.cfg.ProcessStopTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		m.mu.Lock()
		m.lastOperationResult = ctx.Err().Error()
		m.mu.Unlock()
		return ctx.Err()
	case <-done:
		m.mu.Lock()
		m.lastOperationResult = "stopped"
		m.mu.Unlock()
		return nil
	case <-timer.C:
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			m.mu.Lock()
			m.lastOperationResult = err.Error()
			m.mu.Unlock()
			return fmt.Errorf("kill openclaw: %w", err)
		}
		select {
		case <-done:
			m.mu.Lock()
			m.lastOperationResult = "killed_after_timeout"
			m.mu.Unlock()
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return errors.New("openclaw did not exit after kill")
		}
	}
}

func (m *Manager) Restart(ctx context.Context) error {
	if err := m.Stop(ctx); err != nil {
		return err
	}
	return m.Start(ctx)
}

func (m *Manager) MarkConfiguring() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = StatusConfiguring
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := Snapshot{
		Status:              m.status,
		StartedAt:           m.startedAt,
		ExitedAt:            m.exitedAt,
		LastExitCode:        m.lastExitCode,
		LastExitReason:      m.lastExitReason,
		LastOperation:       m.lastOperation,
		LastOperationResult: m.lastOperationResult,
		Restarts:            m.restarts,
	}
	if m.cmd != nil && m.cmd.Process != nil {
		s.PID = m.cmd.Process.Pid
	}
	if !m.startedAt.IsZero() && (m.status == StatusRunning || m.status == StatusStarting || m.status == StatusConfiguring) {
		s.Uptime = time.Since(m.startedAt)
	}
	return s
}

func (m *Manager) checkHealth() bool {
	if m.cfg.OpenClawHealthURL == "" {
		return true
	}
	req, err := http.NewRequest(http.MethodGet, m.cfg.OpenClawHealthURL, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func (m *Manager) wait(cmd *exec.Cmd) {
	err := cmd.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	defer func() {
		if m.done != nil {
			close(m.done)
			m.done = nil
		}
	}()

	m.exitedAt = time.Now().UTC()
	if cmd.ProcessState != nil {
		m.lastExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		m.status = StatusCrashed
		m.lastExitReason = err.Error()
	} else if m.stopRequested || m.status == StatusStopping {
		m.status = StatusStopped
		m.lastExitReason = "stopped_by_agent"
	} else if m.status != StatusStopped {
		m.status = StatusStopped
		m.lastExitReason = "process_exited"
	}
	if m.startedAt.Add(30 * time.Second).Before(m.exitedAt) {
		m.restarts = 0
	} else {
		m.restarts++
	}
	m.cmd = nil
	m.stopRequested = false
}
