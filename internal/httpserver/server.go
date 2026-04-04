package httpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iamlovingit/clawmanager-openclaw-image/internal/openclawinspector"
	"github.com/iamlovingit/clawmanager-openclaw-image/internal/process"
	"github.com/iamlovingit/clawmanager-openclaw-image/internal/profiler"
	"github.com/iamlovingit/clawmanager-openclaw-image/internal/store"
)

type Server struct {
	srv *http.Server
}

func New(bind string, proc *process.Manager, prof *profiler.Profiler, inspector *openclawinspector.Inspector, st *store.Store) *Server {
	router := gin.New()
	router.Use(gin.Recovery())

	router.GET("/healthz", func(c *gin.Context) {
		snapshot := proc.Snapshot()
		code := http.StatusOK
		if snapshot.Status == process.StatusCrashed || snapshot.Status == process.StatusUnknown {
			code = http.StatusServiceUnavailable
		}
		c.JSON(code, gin.H{
			"status":          snapshot.Status,
			"openclaw_pid":    snapshot.PID,
			"current_state":   st.Snapshot(),
			"server_time_utc": time.Now().UTC(),
		})
	})

	router.GET("/readyz", func(c *gin.Context) {
		snapshot := proc.Snapshot()
		code := http.StatusServiceUnavailable
		if snapshot.Status == process.StatusRunning || snapshot.Status == process.StatusStarting {
			code = http.StatusOK
		}
		c.JSON(code, gin.H{
			"status":          snapshot.Status,
			"openclaw_pid":    snapshot.PID,
			"server_time_utc": time.Now().UTC(),
		})
	})

	router.GET("/debug/state", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"process": proc.Snapshot(),
			"store":   st.Snapshot(),
		})
	})

	router.GET("/debug/runtime", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"process":   proc.Snapshot(),
			"system":    prof.Collect(),
			"openclaw":  inspector.Collect(),
			"store":     st.Snapshot(),
			"timestamp": time.Now().UTC(),
		})
	})

	return &Server{
		srv: &http.Server{
			Addr:              bind,
			Handler:           router,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
