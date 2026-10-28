package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	raftpkg "github.com/neman/janus/internal/raft"
	"github.com/neman/janus/internal/server"
)

func main() {
	var (
		nodeID    = flag.String("node-id", "node-1", "Unique node identifier")
		raftAddr  = flag.String("raft-addr", "127.0.0.1:7000", "Raft bind address")
		httpAddr  = flag.String("http-addr", ":8080", "HTTP server address")
		dataDir   = flag.String("data-dir", "./data", "Data directory")
		bootstrap = flag.Bool("bootstrap", false, "Bootstrap new cluster")
	)
	flag.Parse()

	log.Printf("Starting Janus node %s", *nodeID)
	log.Printf("  Raft: %s  HTTP: %s  Data: %s  Bootstrap: %v", *raftAddr, *httpAddr, *dataDir, *bootstrap)

	node, err := raftpkg.NewNode(&raftpkg.Config{
		NodeID:    *nodeID,
		BindAddr:  *raftAddr,
		DataDir:   *dataDir,
		Bootstrap: *bootstrap,
	})
	if err != nil {
		log.Fatalf("Failed to create node: %v", err)
	}
	defer node.Shutdown()

	srv := server.New(node)
	httpServer := &http.Server{Addr: *httpAddr, Handler: srv.Handler()}

	go func() {
		log.Printf("HTTP server listening on %s", *httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	log.Println("Janus is ready. Press Ctrl+C to stop.")
	<-sigCh

	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpServer.Shutdown(ctx)
	log.Println("Shutdown complete.")
}
