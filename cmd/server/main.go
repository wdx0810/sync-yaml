package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/configmap-sync/configmap-sync/internal/api"
	"github.com/configmap-sync/configmap-sync/internal/config"
	"github.com/configmap-sync/configmap-sync/internal/crypto"
	"github.com/configmap-sync/configmap-sync/internal/engine"
	"github.com/configmap-sync/configmap-sync/internal/history"
	"github.com/configmap-sync/configmap-sync/internal/store"
)

//go:embed all:web/dist
var frontendFS embed.FS

func main() {
	configPath := flag.String("config", "", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Resolve storage path.
	storagePath := cfg.History.StoragePath
	if len(storagePath) > 0 && storagePath[0] == '~' {
		home, _ := os.UserHomeDir()
		storagePath = filepath.Join(home, storagePath[1:])
	}
	// Ensure storage directory exists.
	_ = os.MkdirAll(storagePath, 0755)
	logger.Info("data storage path", "path", storagePath)

	// Initialize crypto service.
	cryptoSvc, err := crypto.NewService(filepath.Join(storagePath, "encryption.key"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating crypto service: %v\n", err)
		os.Exit(1)
	}

	// Initialize stores.
	var taskStoreRef store.TaskStore
	userStore := store.NewUserStore(storagePath)
	sourceStore := store.NewSourceStore(storagePath, cryptoSvc, func() store.TaskStore { return taskStoreRef })
	targetStore := store.NewTargetStore(storagePath, cryptoSvc, func() store.TaskStore { return taskStoreRef })
	taskStoreRef = store.NewTaskStore(storagePath, sourceStore, targetStore)

	// Initialize history store.
	historyStore, err := history.NewStore(storagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating history store: %v\n", err)
		os.Exit(1)
	}

	// Initialize connection tester.
	connTester := store.NewConnectionTester()

	// Initialize notify store.
	notifyStore := store.NewNotifyStore(storagePath)

	// Initialize change request store (independent ConfigMap edit → approve → commit module).
	changeReqStore := store.NewChangeRequestStore(storagePath)

	// Initialize task manager.
	taskMgr := engine.NewTaskManager(sourceStore, targetStore, taskStoreRef, historyStore, notifyStore)

	// Initialize API server.
	apiServer := api.NewServer(api.ServerConfig{
		History:     historyStore,
		SourceStore: sourceStore,
		TargetStore: targetStore,
		TaskStore:   taskStoreRef,
		TaskManager: taskMgr,
		ConnTester:  connTester,
		UserStore:   userStore,
		NotifyStore: notifyStore,
		ChangeRequestStore: changeReqStore,
		StoragePath: storagePath,
	})
	router := apiServer.Router()

	// Register static file routes (SPA).
	distFS, err := fs.Sub(frontendFS, "web/dist")
	if err != nil {
		logger.Warn("frontend assets not found")
	} else {
		api.RegisterStaticRoutes(router, distFS)
	}

	// Restore previously running tasks.
	tasks, _ := taskStoreRef.List()
	_ = taskMgr.RestoreRunningTasks(tasks)

	// Start HTTP server.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx

	addr := fmt.Sprintf(":%d", cfg.API.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
		// Read timeout protects against slow clients.
		ReadTimeout: 30 * time.Second,
		// Write timeout must be long enough for full sync of many resources.
		// Apply of dozens of resources, especially with retries, can easily
		// take minutes; the previous 15s caused spurious "network error" on
		// the client side.
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		logger.Info("starting HTTP server", "addr", addr)
		logger.Info("open http://localhost" + addr + " in your browser")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down...")
	cancel()
	taskMgr.StopAll()
	_ = historyStore.Flush()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)

	logger.Info("server stopped")
}
