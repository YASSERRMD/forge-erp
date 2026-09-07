package notify

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal capturing SMTP server for offline tests.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	got  []string // RCPT addresses
	data string
	done chan struct{}
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSMTP{ln: ln, done: make(chan struct{})}
	go f.serve()
	return f
}

func (f *fakeSMTP) addr() string { return f.ln.Addr().String() }

func (f *fakeSMTP) serve() {
	defer close(f.done)
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	rd := bufio.NewReader(conn)
	write := func(s string) { fmt.Fprintf(conn, "%s\r\n", s) }
	write("220 fake ESMTP")
	var data strings.Builder
	inData := false
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				f.mu.Lock()
				f.data = data.String()
				f.mu.Unlock()
				write("250 ok")
				continue
			}
			data.WriteString(line + "\n")
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
			write("250-fake")
			write("250 8BITMIME")
		case strings.HasPrefix(upper, "MAIL FROM"):
			write("250 ok")
		case strings.HasPrefix(upper, "RCPT TO"):
			addr := line[strings.Index(line, ":")+1:]
			addr = strings.Trim(addr, " <>")
			f.mu.Lock()
			f.got = append(f.got, addr)
			f.mu.Unlock()
			write("250 ok")
		case strings.HasPrefix(upper, "DATA"):
			inData = true
			write("354 end with .")
		case strings.HasPrefix(upper, "QUIT"):
			write("221 bye")
			return
		default:
			write("250 ok")
		}
	}
}

func TestSendViaFakeSMTP(t *testing.T) {
	f := newFakeSMTP(t)
	defer func() { _ = f.ln.Close() }()
	host, port := f.addr()[:strings.LastIndex(f.addr(), ":")], 0
	fmt.Sscanf(f.addr()[strings.LastIndex(f.addr(), ":")+1:], "%d", &port)
	s := NewSMTPSender(SMTPConfig{Host: host, Port: port, From: "erp@example.com", Timeout: 5 * time.Second})
	err := s.SendMail(context.Background(), Mail{
		To:      []string{"ada@example.com", "bob@example.com"},
		Subject: "Invoice INV-1 validated",
		Body:    "Hello",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	<-f.done
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) != 2 || f.got[0] != "ada@example.com" {
		t.Fatalf("recipients=%v", f.got)
	}
	if !strings.Contains(f.data, "Subject: Invoice INV-1 validated") {
		t.Fatalf("subject missing in:\n%s", f.data)
	}
}

func TestSendValidation(t *testing.T) {
	s := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: 1, From: "erp@example.com"})
	if err := s.SendMail(context.Background(), Mail{Subject: "x", Body: "y"}); err == nil {
		t.Error("recipient-less mail accepted")
	}
	if err := s.SendMail(context.Background(), Mail{To: []string{"bad"}, Subject: "x"}); err == nil {
		t.Error("bad recipient accepted")
	}
	if err := s.SendMail(context.Background(), Mail{To: []string{"a@b.c"}, Subject: "x", Body: "y"}); err == nil {
		t.Error("unreachable relay accepted")
	}
}

func TestDispatcherUsesSMTPSender(t *testing.T) {
	f := newFakeSMTP(t)
	defer func() { _ = f.ln.Close() }()
	host, port := f.addr()[:strings.LastIndex(f.addr(), ":")], 0
	fmt.Sscanf(f.addr()[strings.LastIndex(f.addr(), ":")+1:], "%d", &port)
	d := Dispatcher{Sender: NewSMTPSender(SMTPConfig{Host: host, Port: port,
		From: "erp@example.com", Timeout: 5 * time.Second})}
	item := &OutboxItem{EntityID: 1, Channel: "email", Recipient: "ada@example.com",
		Subject: "Hi", Body: "hello"}
	if !d.Dispatch(context.Background(), item) {
		t.Fatalf("dispatch failed: %s", item.Error)
	}
	if item.SentAt == nil {
		t.Error("sent stamp missing")
	}
	<-f.done
}
