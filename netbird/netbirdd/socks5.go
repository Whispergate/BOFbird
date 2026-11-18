package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"github.com/netbirdio/netbird/client/embed"
)

const (
	socks5Version     = 0x05
	socks5AuthNone    = 0x00
	socks5AuthNoMatch = 0xFF
	socks5CmdConnect  = 0x01
	socks5AtypIPv4    = 0x01
	socks5AtypDomain  = 0x03
	socks5AtypIPv6    = 0x04
	socks5RepSuccess  = 0x00
	socks5RepFailure  = 0x01
)

// startSOCKS5Server starts a minimal SOCKS5 proxy that uses the NetBird
// embed.Client.Dial() for outbound connections through the overlay network.
func startSOCKS5Server(ctx context.Context, client *embed.Client, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("socks5 listen: %w", err)
	}
	defer listener.Close()

	log.Printf("[+] SOCKS5 proxy listening on %s\n", addr)

	// Close listener when context is done
	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				log.Printf("[!] SOCKS5 accept error: %v\n", err)
				continue
			}
		}
		go handleSOCKS5Conn(ctx, client, conn)
	}
}

func handleSOCKS5Conn(ctx context.Context, client *embed.Client, conn net.Conn) {
	defer conn.Close()

	// 1. Auth negotiation
	// Read: VER | NMETHODS | METHODS...
	buf := make([]byte, 258)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	if buf[0] != socks5Version {
		return
	}
	nMethods := int(buf[1])
	if _, err := io.ReadFull(conn, buf[:nMethods]); err != nil {
		return
	}

	// We only support no-auth
	foundNoAuth := false
	for i := 0; i < nMethods; i++ {
		if buf[i] == socks5AuthNone {
			foundNoAuth = true
			break
		}
	}

	if !foundNoAuth {
		conn.Write([]byte{socks5Version, socks5AuthNoMatch})
		return
	}
	conn.Write([]byte{socks5Version, socks5AuthNone})

	// 2. Read CONNECT request
	// VER | CMD | RSV | ATYP | DST.ADDR | DST.PORT
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return
	}
	if buf[0] != socks5Version || buf[1] != socks5CmdConnect {
		// Only CONNECT supported
		sendSOCKS5Reply(conn, 0x07) // Command not supported
		return
	}

	atyp := buf[3]
	var targetAddr string

	switch atyp {
	case socks5AtypIPv4:
		if _, err := io.ReadFull(conn, buf[:4]); err != nil {
			return
		}
		targetAddr = net.IP(buf[:4]).String()

	case socks5AtypDomain:
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return
		}
		domainLen := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:domainLen]); err != nil {
			return
		}
		targetAddr = string(buf[:domainLen])

	case socks5AtypIPv6:
		if _, err := io.ReadFull(conn, buf[:16]); err != nil {
			return
		}
		targetAddr = net.IP(buf[:16]).String()

	default:
		sendSOCKS5Reply(conn, 0x08) // Address type not supported
		return
	}

	// Read port (2 bytes, big-endian)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(buf[:2])

	target := fmt.Sprintf("%s:%d", targetAddr, port)

	// 3. Dial through NetBird overlay
	remote, err := client.Dial(ctx, "tcp", target)
	if err != nil {
		log.Printf("[!] SOCKS5 dial %s failed: %v\n", target, err)
		sendSOCKS5Reply(conn, socks5RepFailure)
		return
	}
	defer remote.Close()

	// 4. Send success reply
	// Use 0.0.0.0:0 as the bind address (we don't expose the real bind)
	reply := []byte{
		socks5Version, socks5RepSuccess, 0x00, socks5AtypIPv4,
		0, 0, 0, 0, // BND.ADDR
		0, 0, // BND.PORT
	}
	if _, err := conn.Write(reply); err != nil {
		return
	}

	// 5. Bidirectional relay
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(remote, conn)
		remote.Close()
	}()

	go func() {
		defer wg.Done()
		io.Copy(conn, remote)
		conn.Close()
	}()

	wg.Wait()
}

func sendSOCKS5Reply(conn net.Conn, rep byte) {
	reply := []byte{
		socks5Version, rep, 0x00, socks5AtypIPv4,
		0, 0, 0, 0,
		0, 0,
	}
	conn.Write(reply)
}
