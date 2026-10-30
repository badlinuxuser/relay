// Package daemon запускает бэкенд Relay (подкоманда `relay daemon`): провайдеры,
// роутинг, агенты, процессы и OpenAI-совместимый шлюз.
package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"relay/internal/config"
	"relay/internal/proto"
	"relay/internal/server"
)

func loadToken(home string) (string, error) {
	path := filepath.Join(home, "token")
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		return string(b), nil
	}
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw[:])
	return tok, os.WriteFile(path, []byte(tok), 0o600)
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "relay daemon:", err)
	log.Printf("fatal: %v", err)
	return 1
}

// Run выполняет бэкенд в текущем процессе и возвращает код выхода.
func Run(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to config.toml (default: <home>/config.toml)")
	listen := fs.String("listen", "", "listen address, overrides the config")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	home, err := config.Home()
	if err != nil {
		return fail(err)
	}
	if logf, err := os.OpenFile(filepath.Join(home, "relayd.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		log.SetOutput(logf)
		defer logf.Close()
	}
	path := *cfgPath
	if path == "" {
		if path, err = config.Ensure(home); err != nil {
			return fail(err)
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return fail(err)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	token, err := loadToken(home)
	if err != nil {
		return fail(err)
	}

	srv := server.New(cfg, home, token)
	srv.StartMCP()
	for _, p := range cfg.Processes {
		if p.AutoStart {
			if err := srv.Procs().Start(p.Name); err != nil {
				log.Printf("autostart %s: %v", p.Name, err)
			}
		}
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fail(fmt.Errorf("cannot listen on %s: %w", cfg.Listen, err))
	}
	httpSrv := &http.Server{Handler: srv.Handler()}

	infoPath := filepath.Join(home, "daemon.json")
	info, _ := json.Marshal(proto.DaemonInfo{Addr: ln.Addr().String(), Token: token, PID: os.Getpid()})
	if err := os.WriteFile(infoPath, info, 0o600); err != nil {
		return fail(err)
	}
	defer os.Remove(infoPath)

	log.Printf("relay daemon %s listening on %s", proto.Version, ln.Addr())
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	code := 0
	select {
	case <-sig:
	case <-srv.Done:
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("server: %v", err)
			code = 1
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	srv.Close()
	log.Printf("relay daemon stopped")
	return code
}
