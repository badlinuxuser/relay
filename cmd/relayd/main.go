// relayd: бэкенд Relay. Держит провайдеры, роутинг, агентов, процессы и
// OpenAI-совместимый шлюз. Интерфейсы (relay и любые другие) подключаются по HTTP.
package main

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

func main() {
	cfgPath := flag.String("config", "", "path to config.toml (default: <home>/config.toml)")
	listen := flag.String("listen", "", "listen address, overrides the config")
	printCfg := flag.Bool("print-config", false, "print the example config and exit")
	flag.Parse()

	if *printCfg {
		fmt.Print(config.ExampleTOML)
		return
	}

	home, err := config.Home()
	if err != nil {
		log.Fatal(err)
	}
	if logf, err := os.OpenFile(filepath.Join(home, "relayd.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		log.SetOutput(logf)
		defer logf.Close()
	}
	path := *cfgPath
	if path == "" {
		if path, err = config.Ensure(home); err != nil {
			log.Fatal(err)
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		log.Fatal(err)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	token, err := loadToken(home)
	if err != nil {
		log.Fatal(err)
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
		fmt.Fprintln(os.Stderr, "cannot listen on", cfg.Listen+":", err)
		log.Fatal(err)
	}
	httpSrv := &http.Server{Handler: srv.Handler()}

	infoPath := filepath.Join(home, "daemon.json")
	info, _ := json.Marshal(proto.DaemonInfo{Addr: ln.Addr().String(), Token: token, PID: os.Getpid()})
	if err := os.WriteFile(infoPath, info, 0o600); err != nil {
		log.Fatal(err)
	}
	defer os.Remove(infoPath)

	log.Printf("relayd %s listening on %s", proto.Version, ln.Addr())
	go func() {
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-srv.Done:
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	srv.Close()
	log.Printf("relayd stopped")
}
