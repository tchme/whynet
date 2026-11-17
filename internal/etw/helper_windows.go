package etw

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// HelperFlag starts whynet as the elevated UDP helper: `whynet.exe -udp-helper <addr> <key>`.
const HelperFlag = "-udp-helper"

// helperMax: the helper stops by itself after this long, whatever the window does.
const helperMax = 3 * time.Minute

// ErrDeclined means the person said no to the administrator prompt.
var ErrDeclined = errors.New("etw: the administrator prompt was declined")

// Elevated reports whether this process runs as administrator.
func Elevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// Stream reads UDP until stop is called: in this process when it runs as administrator,
// otherwise through an elevated copy of this exe started with a UAC prompt (ErrDeclined when the
// person refuses). onPacket is called from one goroutine at a time.
func Stream(onPacket func(Packet)) (stop func(), err error) {
	if Elevated() {
		s, err := Start(sessionName(), onPacket)
		if err != nil {
			return nil, err
		}
		return s.Stop, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	k := make([]byte, 16)
	rand.Read(k)
	key := hex.EncodeToString(k)
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// ShellExecute "runas" returns once the prompt is answered.
	if err := windows.ShellExecute(0, windows.StringToUTF16Ptr("runas"), windows.StringToUTF16Ptr(exe),
		windows.StringToUTF16Ptr(HelperFlag+" "+ln.Addr().String()+" "+key), nil, windows.SW_HIDE); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return nil, ErrDeclined
		}
		return nil, err
	}
	conn, rd, err := accept(ln.(*net.TCPListener), key, 15*time.Second)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	done := make(chan struct{})
	go func() {
		defer close(done)
		dec := json.NewDecoder(rd)
		for {
			var p Packet
			if dec.Decode(&p) != nil {
				return
			}
			onPacket(p)
		}
	}()
	return func() {
		once.Do(func() {
			conn.Close() // the helper sees it and exits
			<-done
		})
	}, nil
}

// accept waits for the helper: the first line must be the key; anything else is dropped. The
// reader carries on after the key: packets may already sit behind it.
func accept(ln *net.TCPListener, key string, wait time.Duration) (net.Conn, *bufio.Reader, error) {
	ln.SetDeadline(time.Now().Add(wait))
	for {
		c, err := ln.Accept()
		if err != nil {
			return nil, nil, errors.New("etw: the helper did not connect: " + err.Error())
		}
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		rd := bufio.NewReader(c)
		line, err := rd.ReadString('\n')
		if err == nil && strings.TrimSpace(line) == key {
			c.SetReadDeadline(time.Time{})
			return c, rd, nil
		}
		c.Close()
	}
}

// RunHelper is the elevated side: it connects to the window, proves itself with the key and sends
// every UDP packet until the window closes the connection (or exits), or helperMax passes.
func RunHelper(addr, key string) error {
	if host, _, err := net.SplitHostPort(addr); err != nil || host != "127.0.0.1" {
		return errors.New("etw: the helper only talks to 127.0.0.1")
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(key + "\n")); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), helperMax)
	defer cancel()
	go func() { // the window never writes again: a read returns when it closes the connection or dies
		conn.Read(make([]byte, 1))
		cancel()
	}()

	w := bufio.NewWriter(conn)
	enc := json.NewEncoder(w)
	var mu sync.Mutex
	s, err := Start(sessionName(), func(p Packet) {
		if !p.Remote.Addr().IsGlobalUnicast() {
			return // multicast, loopback, link-local: never a server
		}
		mu.Lock()
		enc.Encode(p)
		mu.Unlock()
	})
	if err != nil {
		return err
	}
	defer s.Stop()
	flush := time.NewTicker(200 * time.Millisecond)
	defer flush.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-flush.C:
			mu.Lock()
			err := w.Flush()
			mu.Unlock()
			if err != nil {
				return nil
			}
		}
	}
}

// sessionName is per process, so a second whynet (or a leftover helper) gets its own session.
func sessionName() string { return "whynet-udp-" + strconv.Itoa(os.Getpid()) }
