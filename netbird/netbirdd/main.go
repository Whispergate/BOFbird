//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"github.com/netbirdio/netbird/client/embed"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")

func main() {
	setupKey := flag.String("setup-key", "", "NetBird setup key for authentication")
	managementURL := flag.String("management-url", "", "NetBird management server URL")
	hostname := flag.String("hostname", "", "Device name in the NetBird network")
	socks5Addr := flag.String("socks5-server", "", "Address to run SOCKS5 proxy on (e.g. localhost:1080)")
	socketPath := flag.String("socket", "", "Named pipe path for LocalAPI (default: random UUID)")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	flag.Parse()

	if *setupKey == "" {
		log.Println("[!] --setup-key is required")
		return
	}
	if *managementURL == "" {
		log.Println("[!] --management-url is required")
		return
	}

	// Generate random pipe path if not specified
	pipePath := *socketPath
	if pipePath == "" {
		pipePath = fmt.Sprintf(`\\.\pipe\%s`, uuid.New().String())
		log.Printf("[=] No socket provided, using random pipe: %s\n", pipePath)
	}

	// Create the embedded NetBird client
	opts := embed.Options{
		SetupKey:      *setupKey,
		ManagementURL: *managementURL,
		DeviceName:    *hostname,
		LogLevel:      *logLevel,
	}

	client, err := embed.New(opts)
	if err != nil {
		log.Printf("[!] Failed to create NetBird client: %v\n", err)
		return
	}

	// Start the client
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Println("[*] Starting NetBird client...")
	if err := client.Start(ctx); err != nil {
		log.Printf("[!] Failed to start NetBird client: %v\n", err)
		return
	}
	log.Println("[+] NetBird client started")

	// Print initial status
	status, err := client.Status()
	if err == nil {
		statusJSON, _ := json.MarshalIndent(status, "", "  ")
		log.Printf("[=] Initial status:\n%s\n", string(statusJSON))
	}

	// Start SOCKS5 proxy if requested
	if *socks5Addr != "" {
		log.Printf("[*] Starting SOCKS5 proxy on %s\n", *socks5Addr)
		go func() {
			if err := startSOCKS5Server(ctx, client, *socks5Addr); err != nil {
				log.Printf("[!] SOCKS5 server error: %v\n", err)
			}
		}()
	}

	// Start LocalAPI server on the named pipe
	log.Printf("[*] Starting LocalAPI on %s\n", pipePath)
	apiServer := NewLocalAPIServer(client, pipePath)
	go func() {
		if err := apiServer.Serve(ctx); err != nil {
			log.Printf("[!] LocalAPI server error: %v\n", err)
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("[*] Shutting down...")
	apiServer.Close()
	if err := client.Stop(context.Background()); err != nil {
		log.Printf("[!] Error stopping client: %v\n", err)
	}
	log.Println("[+] Shutdown complete")
}
