package logging

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitTimeout bounds every assertion that has to wait for delivery so a broken
// implementation fails the test instead of hanging it.
const waitTimeout = 3 * time.Second

// recvLine waits for one broadcast line.
func recvLine(t *testing.T, ch <-chan []byte) []byte {
	t.Helper()
	select {
	case line, ok := <-ch:
		if !ok {
			t.Fatal("subscriber channel closed unexpectedly")
		}
		return line
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for broadcast line")
		return nil
	}
}

// decodeLog unmarshals one JSON log line.
func decodeLog(t *testing.T, line []byte) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(line, &rec); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, line)
	}
	return rec
}

func TestBroadcastHandler_WritesAndBroadcasts(t *testing.T) {
	var out bytes.Buffer
	b := NewBroadcaster(4)
	h := NewBroadcastHandler(&out, slog.LevelDebug, b)
	logger := slog.New(h)

	sub := b.Subscribe()
	defer sub.Close()

	logger.Info("hello", "key", "value")

	line := recvLine(t, sub.C())
	rec := decodeLog(t, line)
	if rec["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", rec["msg"])
	}
	if rec["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", rec["level"])
	}
	if rec["key"] != "value" {
		t.Errorf("key = %v, want value", rec["key"])
	}
	if _, ok := rec["time"]; !ok {
		t.Error("broadcast entry is missing a timestamp")
	}

	// The terminal output must carry the same JSON plus a trailing newline.
	wantOut := string(line) + "\n"
	if got := out.String(); got != wantOut {
		t.Errorf("stderr = %q, want %q", got, wantOut)
	}
}

func TestBroadcastHandler_LevelFiltering(t *testing.T) {
	var out bytes.Buffer
	b := NewBroadcaster(4)
	logger := slog.New(NewBroadcastHandler(&out, slog.LevelWarn, b))

	sub := b.Subscribe()
	defer sub.Close()

	logger.Debug("debug")
	logger.Info("info")
	if b.Subscribers() != 1 {
		t.Fatalf("subscribers = %d, want 1", b.Subscribers())
	}
	select {
	case line := <-sub.C():
		t.Fatalf("filtered record was broadcast: %q", line)
	case <-time.After(50 * time.Millisecond):
	}

	logger.Warn("warn")
	rec := decodeLog(t, recvLine(t, sub.C()))
	if rec["level"] != "WARN" || rec["msg"] != "warn" {
		t.Errorf("got %v/%v, want WARN/warn", rec["level"], rec["msg"])
	}
	if strings.Contains(out.String(), "INFO") || strings.Contains(out.String(), "debug") {
		t.Errorf("filtered records reached stderr: %q", out.String())
	}
}

func TestBroadcastHandler_WithAttrsAndGroup(t *testing.T) {
	var out bytes.Buffer
	b := NewBroadcaster(4)
	h := NewBroadcastHandler(&out, slog.LevelDebug, b)
	logger := slog.New(h).With("svc", "proxy").WithGroup("req").With("id", 7)

	sub := b.Subscribe()
	defer sub.Close()

	logger.Info("done", "status", 200)

	rec := decodeLog(t, recvLine(t, sub.C()))
	if rec["svc"] != "proxy" {
		t.Errorf("svc = %v, want proxy", rec["svc"])
	}
	group, ok := rec["req"].(map[string]any)
	if !ok {
		t.Fatalf("req group missing or not an object: %v", rec["req"])
	}
	if group["id"] != float64(7) {
		t.Errorf("req.id = %v, want 7", group["id"])
	}
	if group["status"] != float64(200) {
		t.Errorf("req.status = %v, want 200", group["status"])
	}
}

func TestBroadcastHandler_NilSinks(t *testing.T) {
	// Neither a writer nor a broadcaster is required: the handler must simply
	// discard records instead of panicking.
	h := NewBroadcastHandler(nil, nil, nil)
	logger := slog.New(h)
	logger.Debug("silent") // below the default info level
	logger.Info("quiet")
}

func TestBroadcastHandler_Enabled(t *testing.T) {
	tests := []struct {
		name  string
		level slog.Leveler
		in    slog.Level
		want  bool
	}{
		{"nil defaults to info", nil, slog.LevelInfo, true},
		{"nil filters debug", nil, slog.LevelDebug, false},
		{"debug allows itself", slog.LevelDebug, slog.LevelDebug, true},
		{"debug filters trace", slog.LevelDebug, slog.Level(-8), false},
		{"error filters warn", slog.LevelError, slog.LevelWarn, false},
		{"error allows error", slog.LevelError, slog.LevelError, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewBroadcastHandler(io.Discard, tt.level, nil)
			if got := h.Enabled(context.Background(), tt.in); got != tt.want {
				t.Errorf("Enabled(%s) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestBroadcaster_FanOut(t *testing.T) {
	b := NewBroadcaster(4)
	a := b.Subscribe()
	c := b.Subscribe()
	defer a.Close()
	defer c.Close()

	if got := b.Subscribers(); got != 2 {
		t.Fatalf("subscribers = %d, want 2", got)
	}

	b.Broadcast([]byte(`{"msg":"one"}`))
	b.Broadcast([]byte(`{"msg":"two"}`))

	for _, sub := range []*Subscriber{a, c} {
		if got := string(recvLine(t, sub.C())); got != `{"msg":"one"}` {
			t.Errorf("first line = %q, want one", got)
		}
		if got := string(recvLine(t, sub.C())); got != `{"msg":"two"}` {
			t.Errorf("second line = %q, want two", got)
		}
		if dropped := sub.Dropped(); dropped != 0 {
			t.Errorf("dropped = %d, want 0", dropped)
		}
	}
}

func TestBroadcaster_SlowSubscriberDropsInsteadOfBlocking(t *testing.T) {
	b := NewBroadcaster(1)
	slow := b.Subscribe()
	defer slow.Close()

	const total = 100
	start := time.Now()
	for i := 0; i < total; i++ {
		b.Broadcast([]byte(fmt.Sprintf("line-%d", i)))
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Broadcast took %v; a full subscriber must not block the caller", elapsed)
	}

	// The subscriber was never drained, so only the first line fits and the
	// rest are counted as drops.
	if got := string(recvLine(t, slow.C())); got != "line-0" {
		t.Errorf("buffered line = %q, want line-0", got)
	}
	if got := slow.Dropped(); got != total-1 {
		t.Errorf("dropped = %d, want %d", got, total-1)
	}
	select {
	case line := <-slow.C():
		t.Fatalf("unexpected buffered line %q", line)
	default:
	}
}

func TestBroadcaster_Unsubscribe(t *testing.T) {
	b := NewBroadcaster(2)
	sub := b.Subscribe()
	b.Unsubscribe(sub)

	if got := b.Subscribers(); got != 0 {
		t.Fatalf("subscribers = %d, want 0", got)
	}
	if _, ok := <-sub.C(); ok {
		t.Fatal("channel should be closed after unsubscribe")
	}

	// Repeated release must be safe and broadcast must not panic.
	b.Unsubscribe(sub)
	sub.Close()
	b.Broadcast([]byte("ignored"))
}

func TestBroadcaster_ConcurrentUse(t *testing.T) {
	b := NewBroadcaster(8)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			b.Broadcast([]byte(`{"msg":"concurrent"}`))
		}
	}()

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				sub := b.Subscribe()
				select {
				case <-sub.C():
				default:
				}
				sub.Close()
			}
		}()
	}

	wg.Wait()
	if got := b.Subscribers(); got != 0 {
		t.Fatalf("subscribers = %d, want 0 after all closed", got)
	}
}

// sseReader reads SSE events from a stream with a bounded wait.
type sseReader struct {
	br *bufio.Reader
}

func newSSEReader(r io.Reader) *sseReader {
	return &sseReader{br: bufio.NewReader(r)}
}

// next returns the payload of the next "data:" event.
func (s *sseReader) next(t *testing.T) string {
	t.Helper()
	type result struct {
		data string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := s.br.ReadString('\n')
		if err != nil {
			done <- result{err: err}
			return
		}
		if !strings.HasPrefix(line, "data: ") {
			done <- result{err: fmt.Errorf("unexpected SSE line %q", line)}
			return
		}
		if _, err := s.br.ReadString('\n'); err != nil {
			done <- result{err: err}
			return
		}
		done <- result{data: strings.TrimSuffix(strings.TrimPrefix(line, "data: "), "\n")}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("read SSE event: %v", res.err)
		}
		return res.data
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for SSE event")
		return ""
	}
}

func TestSSEHandler_StreamsLogLines(t *testing.T) {
	b := NewBroadcaster(8)
	logger := slog.New(NewBroadcastHandler(io.Discard, slog.LevelDebug, b))

	srv := httptest.NewServer(b.SSEHandler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}

	events := newSSEReader(resp.Body)

	logger.Info("first", "a", 1)
	rec := decodeLog(t, []byte(events.next(t)))
	if rec["msg"] != "first" || rec["a"] != float64(1) {
		t.Errorf("first event = %v, want msg=first a=1", rec)
	}

	logger.Warn("second")
	rec = decodeLog(t, []byte(events.next(t)))
	if rec["msg"] != "second" || rec["level"] != "WARN" {
		t.Errorf("second event = %v, want msg=second level=WARN", rec)
	}
}

func TestSSEHandler_RejectsNonGET(t *testing.T) {
	b := NewBroadcaster(2)
	rec := httptest.NewRecorder()
	b.SSEHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/logs", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestSSEHandler_UnsubscribesOnDisconnect(t *testing.T) {
	b := NewBroadcaster(8)
	srv := httptest.NewServer(b.SSEHandler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}

	// The handler subscribes before flushing headers, so the client can rely on
	// the subscription existing once the response arrives.
	if got := b.Subscribers(); got != 1 {
		t.Fatalf("subscribers = %d, want 1", got)
	}

	cancel()
	resp.Body.Close()

	deadline := time.Now().Add(waitTimeout)
	for b.Subscribers() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := b.Subscribers(); got != 0 {
		t.Fatalf("subscribers = %d after disconnect, want 0", got)
	}

	// Broadcasting after the client left must still be safe.
	b.Broadcast([]byte(`{"msg":"after"}`))
}

func TestSetup_InstallsBroadcastHandler(t *testing.T) {
	var got []byte
	captureStderr(t, func() {
		Setup("info")
		sub := DefaultBroadcaster().Subscribe()
		defer sub.Close()

		slog.Info("setup-check", "n", 1)

		select {
		case line, ok := <-sub.C():
			if ok {
				got = append([]byte(nil), line...)
			}
		case <-time.After(waitTimeout):
		}
	})

	rec := decodeLog(t, got)
	if rec["msg"] != "setup-check" || rec["n"] != float64(1) {
		t.Errorf("broadcast record = %v, want msg=setup-check n=1", rec)
	}
}

func TestSetup_SSEHandlerStreamsDefaultLogger(t *testing.T) {
	srv := httptest.NewServer(SSEHandler())
	defer srv.Close()

	// Setup writes to stderr; keep that out of the test log. The handler stays
	// installed after captureStderr restores os.Stderr, which is what this test
	// exercises.
	captureStderr(t, func() { Setup("info") })

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	events := newSSEReader(resp.Body)
	slog.Info("via-default", "k", "v")

	rec := decodeLog(t, []byte(events.next(t)))
	if rec["msg"] != "via-default" || rec["k"] != "v" {
		t.Errorf("event = %v, want msg=via-default k=v", rec)
	}
}
