package command

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	configmanager "github.com/iamlovingit/clawmanager-openclaw-image/internal/configmanager"
	"github.com/iamlovingit/clawmanager-openclaw-image/internal/process"
	"github.com/iamlovingit/clawmanager-openclaw-image/internal/profiler"
	"github.com/iamlovingit/clawmanager-openclaw-image/internal/protocol"
	"github.com/iamlovingit/clawmanager-openclaw-image/internal/store"
)

type lifecycleClient interface {
	StartCommand(ctx context.Context, id int, req protocol.CommandStartRequest) error
	FinishCommand(ctx context.Context, id int, req protocol.CommandFinishRequest) error
}

type Executor struct {
	client   lifecycleClient
	process  *process.Manager
	profiler *profiler.Profiler
	config   *configmanager.Manager
	store    *store.Store
	mu       sync.Mutex
}

func New(client lifecycleClient, processManager *process.Manager, profilerInstance *profiler.Profiler, configManager *configmanager.Manager, st *store.Store) *Executor {
	return &Executor{
		client:   client,
		process:  processManager,
		profiler: profilerInstance,
		config:   configManager,
		store:    st,
	}
}

func (e *Executor) Execute(ctx context.Context, cmd *protocol.Command) error {
	if cmd == nil {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	timeout := time.Duration(cmd.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := e.client.StartCommand(runCtx, cmd.ID, protocol.CommandStartRequest{
		AgentID:   e.store.Snapshot().AgentID,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}

	result, execErr := e.handle(runCtx, cmd)
	finishReq := protocol.CommandFinishRequest{
		AgentID:    e.store.Snapshot().AgentID,
		FinishedAt: time.Now().UTC(),
		Status:     "succeeded",
		Result:     result,
	}
	if execErr != nil {
		finishReq.Status = "failed"
		finishReq.ErrorMessage = execErr.Error()
		finishReq.Result = result
	}

	if err := e.store.Update(func(s *store.State) {
		s.LastCommandExecutionCache[strconv.Itoa(cmd.ID)] = map[string]any{
			"type":        cmd.Type,
			"status":      finishReq.Status,
			"finished_at": finishReq.FinishedAt,
			"result":      finishReq.Result,
			"error":       finishReq.ErrorMessage,
		}
	}); err != nil && execErr == nil {
		execErr = err
	}

	if err := e.client.FinishCommand(context.Background(), cmd.ID, finishReq); err != nil && execErr == nil {
		execErr = err
	}
	return execErr
}

func (e *Executor) handle(ctx context.Context, cmd *protocol.Command) (map[string]any, error) {
	switch cmd.Type {
	case "start_openclaw":
		if err := e.process.Start(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"status": e.process.Snapshot().Status}, nil
	case "stop_openclaw":
		if err := e.process.Stop(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"status": e.process.Snapshot().Status}, nil
	case "restart_openclaw":
		if err := e.process.Restart(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"status": e.process.Snapshot().Status}, nil
	case "collect_system_info":
		return e.profiler.Collect(), nil
	case "apply_config_revision":
		revisionID := revisionIDFromPayload(cmd.Payload)
		if revisionID == "" {
			return nil, errors.New("revision_id is required")
		}
		e.process.MarkConfiguring()
		result, err := e.config.ApplyRevision(ctx, revisionID)
		if err != nil {
			return result, err
		}
		if reload, _ := cmd.Payload["reload"].(bool); reload {
			if err := e.process.Restart(ctx); err != nil {
				return result, err
			}
			result["restarted"] = true
		}
		result["status"] = e.process.Snapshot().Status
		return result, nil
	case "reload_config":
		if err := e.process.Restart(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"status": e.process.Snapshot().Status}, nil
	case "health_check":
		snapshot := e.process.Snapshot()
		return map[string]any{
			"status": snapshot.Status,
			"pid":    snapshot.PID,
			"uptime": snapshot.Uptime.String(),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported command type %q", cmd.Type)
	}
}

func revisionIDFromPayload(payload map[string]any) string {
	if value, ok := payload["revision_id"].(string); ok && value != "" {
		return value
	}
	if value, ok := payload["revision_id"].(float64); ok {
		return strconv.Itoa(int(value))
	}
	return ""
}
