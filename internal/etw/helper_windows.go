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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// HelperFlag starts whynet as the elevated helper:
// `whynet.exe -helper <addr> <key> <udp|frames|udp,frames> <seconds>`.
const HelperFlag = "-helper"

// helperCap: the helper stops by itself after the time it was given, never later than this,
// whatever the window does (a stability check records 1 hour at most).
const helperCap = 61 * time.Minute

// ErrDeclined means the person said no to the administrator prompt.
var ErrDeclined = errors.New("etw: the administrator prompt was declined")

// Elevated reports whether this process runs as administrator.
func Elevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// line is one line of the helper's stream: a datagram or a frame.
type line struct {
	U *Packet `json:",omitempty"`
	F *Frame  `json:",omitempty"`
}

func jobs(h Handlers) string {
	var j []string
	if h.Packet != nil {
		j = append(j, "udp")
	}
	if h.Frame != nil {
		j = append(j, "frames")
	}
	return strings.Join(j, ",")
}

// Stream records what h asks for until stop is called, or for limit at most: in this process when
// it runs as administrator, otherwise through an elevated copy of this exe started with a UAC
// prompt (ErrDeclined when the person refuses). h's funcs are called from one goroutine at a time.
func Stream(h Handlers, limit time.Duration) (stop func(), err error) {
	if h.Packet == nil && h.Frame == nil {
		return nil, errors.New("etw: nothing to record")
	}
	if Elevated() {
		s, err := Start(sessionName(), h)
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
	args := strings.Join([]string{HelperFlag, ln.Addr().String(), key, jobs(h), strconv.Itoa(int(limit.Seconds()))}, " ")
	if err := windows.ShellExecute(0, windows.StringToUTF16Ptr("runas"), windows.StringToUTF16Ptr(exe),
		windows.StringToUTF16Ptr(args), nil, windows.SW_HIDE); err != nil {
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
			var l line
			if dec.Decode(&l) != nil {
				return
			}
			if l.U != nil && h.Packet != nil {
				h.Packet(*l.U)
			}
			if l.F != nil && h.Frame != nil {
				h.Frame(*l.F)
			}
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

// HelperMain runs the elevated helper when args (os.Args[1:]) start with HelperFlag; ran is false
// otherwise, and the caller starts as usual.
func HelperMain(args []string) (ran bool, err error) {
	if len(args) != 5 || args[0] != HelperFlag {
		return false, nil
	}
	secs, err := strconv.Atoi(args[4])
	if err != nil || secs <= 0 {
		return true, errors.New("etw: bad helper time limit")
	}
	return true, runHelper(args[1], args[2], strings.Split(args[3], ","), time.Duration(secs)*time.Second)
}

// runHelper is the elevated side: it connects to the window, proves itself with the key and sends
// every UDP packet and / or frame until the window closes the connection (or exits), or limit passes.
func runHelper(addr, key string, jobs []string, limit time.Duration) error {
	if host, _, err := net.SplitHostPort(addr); err != nil || host != "127.0.0.1" {
		return errors.New("etw: the helper only talks to 127.0.0.1")
	}
	udp, frames := slices.Contains(jobs, "udp"), slices.Contains(jobs, "frames")
	if !udp && !frames {
		return errors.New("etw: the helper was given nothing to record")
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(key + "\n")); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), min(limit, helperCap))
	defer cancel()
	go func() { // the window never writes again: a read returns when it closes the connection or dies
		conn.Read(make([]byte, 1))
		cancel()
	}()

	w := bufio.NewWriter(conn)
	enc := json.NewEncoder(w)
	var mu sync.Mutex
	send := func(l line) {
		mu.Lock()
		enc.Encode(l)
		mu.Unlock()
	}
	var h Handlers
	if udp {
		h.Packet = func(p Packet) {
			if p.Remote.Addr().IsGlobalUnicast() { // multicast, loopback, link-local: never a server
				send(line{U: &p})
			}
		}
	}
	if frames {
		h.Frame = func(f Frame) { send(line{F: &f}) }
	}
	s, err := Start(sessionName(), h)
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
func sessionName() string { return "whynet-etw-" + strconv.Itoa(os.Getpid()) }
