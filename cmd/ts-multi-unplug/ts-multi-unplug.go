package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/tailscale/hujson"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"

	_ "modernc.org/sqlite"
)

var (
	flagConfig     = flag.String("config", "", "path to hujson config file (required)")
	flagDB         = flag.String("db", "", "path to SQLite database file (required)")
	flagLogLevel   = flag.String("log", "info", "log level: debug|info|warn|error")
	flagDebugTSNet = flag.Bool("debug-tsnet", false, "enable tsnet.Server logging")
)

type Config struct {
	Servers []ServerConfig `json:"servers"`
}

type ServerConfig struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	LocalPort  int    `json:"local_port"`
	RemoteAddr string `json:"remote_addr"`
}

// SQLiteStore implements ipn.StateStore using SQLite
type SQLiteStore struct {
	db       *sql.DB
	serverID string
}

func (s *SQLiteStore) ReadState(id ipn.StateKey) ([]byte, error) {
	var value []byte
	err := s.db.QueryRow(
		"SELECT value FROM state WHERE server_id = ? AND key = ?",
		s.serverID, string(id),
	).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, ipn.ErrStateNotExist
	}
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (s *SQLiteStore) WriteState(id ipn.StateKey, bs []byte) error {
	_, err := s.db.Exec(
		`INSERT INTO state (server_id, key, value) VALUES (?, ?, ?)
		 ON CONFLICT(server_id, key) DO UPDATE SET value = excluded.value`,
		s.serverID, string(id), bs,
	)
	return err
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	// Standardize hujson to regular JSON
	standardized, err := hujson.Standardize(data)
	if err != nil {
		return nil, fmt.Errorf("parsing hujson: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(standardized, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	return &cfg, nil
}

func initDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS state (
			server_id TEXT NOT NULL,
			key TEXT NOT NULL,
			value BLOB,
			PRIMARY KEY (server_id, key)
		)
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("creating table: %w", err)
	}

	return db, nil
}

func hasState(db *sql.DB, serverID string) bool {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM state WHERE server_id = ?", serverID).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

func startServer(ctx context.Context, db *sql.DB, cfg ServerConfig, debugTSNet bool) error {
	store := &SQLiteStore{db: db, serverID: cfg.ID}

	ts := &tsnet.Server{
		Hostname: cfg.Name,
		Store:    store,
	}

	if debugTSNet {
		ts.Logf = func(format string, args ...any) {
			cur := slog.SetLogLoggerLevel(slog.LevelDebug)
			slog.Debug(fmt.Sprintf("[%s] %s", cfg.Name, fmt.Sprintf(format, args...)))
			slog.SetLogLoggerLevel(cur)
		}
	}

	st, err := ts.Up(ctx)
	if err != nil {
		return fmt.Errorf("starting tsnet server: %w", err)
	}

	slog.Info("tsnet server started",
		slog.String("id", cfg.ID),
		slog.String("name", cfg.Name),
		slog.String("status", st.BackendState),
	)

	// Ensure remote address has a port
	remoteAddr := cfg.RemoteAddr
	if _, _, err := net.SplitHostPort(remoteAddr); err != nil {
		remoteAddr = net.JoinHostPort(remoteAddr, "80")
	}

	target, err := url.Parse("http://" + remoteAddr)
	if err != nil {
		return fmt.Errorf("invalid remote address: %w", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return ts.Dial(ctx, network, remoteAddr)
		},
	}

	listenAddr := fmt.Sprintf("localhost:%d", cfg.LocalPort)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", listenAddr, err)
	}

	slog.Info("HTTP proxy listening",
		slog.String("id", cfg.ID),
		slog.String("local", listenAddr),
		slog.String("remote", remoteAddr),
	)

	server := &http.Server{Handler: proxy}

	go func() {
		<-ctx.Done()
		server.Close()
		listener.Close()
		ts.Close()
	}()

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP server error: %w", err)
	}

	return nil
}

func main() {
	flag.Parse()

	// Set log level
	switch *flagLogLevel {
	case "debug":
		slog.SetLogLoggerLevel(slog.LevelDebug)
	case "info":
		slog.SetLogLoggerLevel(slog.LevelInfo)
	case "warn":
		slog.SetLogLoggerLevel(slog.LevelWarn)
	case "error":
		slog.SetLogLoggerLevel(slog.LevelError)
	default:
		slog.Error("unknown log level", slog.String("level", *flagLogLevel))
		os.Exit(1)
	}

	if *flagConfig == "" {
		slog.Error("-config is required")
		os.Exit(1)
	}
	if *flagDB == "" {
		slog.Error("-db is required")
		os.Exit(1)
	}

	cfg, err := loadConfig(*flagConfig)
	if err != nil {
		slog.Error("failed to load config", slog.Any("error", err))
		os.Exit(1)
	}

	if len(cfg.Servers) == 0 {
		slog.Error("no servers defined in config")
		os.Exit(1)
	}

	db, err := initDB(*flagDB)
	if err != nil {
		slog.Error("failed to initialize database", slog.Any("error", err))
		os.Exit(1)
	}
	defer db.Close()

	ctx, cancelCtx := context.WithCancel(context.Background())

	// Setup signal handling
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-signalChan
		slog.Info("signal received, shutting down...", slog.String("signal", sig.String()))
		cancelCtx()
	}()

	// Partition servers by whether they have existing state
	var withState, withoutState []ServerConfig
	for _, srv := range cfg.Servers {
		if hasState(db, srv.ID) {
			withState = append(withState, srv)
		} else {
			withoutState = append(withoutState, srv)
		}
	}

	slog.Info("starting servers",
		slog.Int("with_state", len(withState)),
		slog.Int("without_state", len(withoutState)),
	)

	var wg sync.WaitGroup

	// Start servers WITHOUT state sequentially (they need interactive auth)
	for _, srv := range withoutState {
		slog.Info("starting new server (needs authentication)", slog.String("id", srv.ID), slog.String("name", srv.Name))

		// Check if context was cancelled
		if ctx.Err() != nil {
			break
		}

		// Start and wait for it to be ready before moving to next
		wg.Add(1)
		srv := srv // capture for goroutine
		go func() {
			defer wg.Done()
			if err := startServer(ctx, db, srv, *flagDebugTSNet); err != nil {
				slog.Error("server failed", slog.String("id", srv.ID), slog.Any("error", err))
			}
		}()

		// Wait for this server to have state before starting the next
		// Poll until state exists or context is cancelled
		for !hasState(db, srv.ID) && ctx.Err() == nil {
			// Small sleep to avoid busy loop
			select {
			case <-ctx.Done():
			default:
			}
		}
	}

	// Start servers WITH state in parallel
	for _, srv := range withState {
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		srv := srv // capture for goroutine
		go func() {
			defer wg.Done()
			if err := startServer(ctx, db, srv, *flagDebugTSNet); err != nil {
				slog.Error("server failed", slog.String("id", srv.ID), slog.Any("error", err))
			}
		}()
	}

	// Wait for all servers to finish (typically due to context cancellation)
	wg.Wait()
	slog.Info("all servers stopped")
}
