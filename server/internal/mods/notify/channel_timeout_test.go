package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	np "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPStallRespectsCancellation(t *testing.T) {
	for _, implicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "smtp_greeting", true: "tls_handshake"}[implicit], func(t *testing.T) {
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			accepted := make(chan net.Conn, 1)
			go func() {
				conn, e := listener.Accept()
				if e == nil {
					accepted <- conn
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			start := time.Now()
			e = sendSMTP(ctx, listener.Addr().String(), "localhost", nil, "from@example.test", "to@example.test", "test", implicit)
			if e == nil || time.Since(start) > 2*time.Second {
				t.Fatal("stalled provider was not bounded", e)
			}
			select {
			case conn := <-accepted:
				conn.Close()
			case <-time.After(time.Second):
				t.Fatal("connection not accepted")
			}
		})
	}
}

func TestSMTPDisplayNameDoesNotEnterEnvelope(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	mail := make(chan string, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprint(conn, "220 localhost\r\n")
		reader := bufio.NewReader(conn)
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				return
			}
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "MAIL FROM:"):
				mail <- line
				fmt.Fprint(conn, "250 ok\r\n")
			case line == "DATA":
				fmt.Fprint(conn, "354 data\r\n")
				for {
					line, e = reader.ReadString('\n')
					if e != nil {
						return
					}
					if line == ".\r\n" {
						break
					}
				}
				fmt.Fprint(conn, "250 accepted\r\n")
			case line == "QUIT":
				fmt.Fprint(conn, "221 bye\r\n")
				return
			default:
				fmt.Fprint(conn, "250 localhost\r\n")
			}
		}
	}()
	raw, _ := json.Marshal(np.SMTPConfig{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Enabled: true, From: "from@example.test", FromName: "Market Support"})
	if e = NewEmailChannel(fakeSettings{raw: raw}).Deliver(context.Background(), np.Message{Recipient: "to@example.test", Subject: "acceptance", Body: "test"}); e != nil {
		t.Fatal(e)
	}
	select {
	case line := <-mail:
		if line != "MAIL FROM:<from@example.test>" {
			t.Fatal("invalid SMTP envelope", line)
		}
	case <-time.After(time.Second):
		t.Fatal("missing envelope")
	}
}
