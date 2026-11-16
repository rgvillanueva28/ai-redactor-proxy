package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// oneByteReader yields a single byte per Read call, exercising the parser's
// buffering and partial-read handling.
type oneByteReader struct {
	data []byte
	pos  int
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.data[r.pos]
	r.pos++
	return 1, nil
}

func parseAll(t *testing.T, r io.Reader) []SSEEvent {
	t.Helper()
	var got []SSEEvent
	if err := ParseEvents(r, func(ev SSEEvent) error {
		got = append(got, ev)
		return nil
	}); err != nil {
		t.Fatalf("ParseEvents: %v", err)
	}
	return got
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func assertEvents(t *testing.T, got, want []SSEEvent) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestParseEvents_Basic(t *testing.T) {
	got := parseAll(t, bytes.NewReader(readFixture(t, "sse_basic.txt")))
	want := []SSEEvent{
		{Data: `{"id":"chatcmpl-1","choices":[{"delta":{"content":"Hello"}}]}`},
		{Data: `{"id":"chatcmpl-1","choices":[{"delta":{"content":" world"}}]}`},
		{Data: "[DONE]"},
	}
	assertEvents(t, got, want)
}

func TestParseEvents_MultiLineData(t *testing.T) {
	got := parseAll(t, bytes.NewReader(readFixture(t, "sse_multiline.txt")))
	want := []SSEEvent{
		{Event: "message", ID: "evt-1", Data: "line1\nline2"},
	}
	assertEvents(t, got, want)
}

func TestParseEvents_WithEventAndID(t *testing.T) {
	stream := "event: message\nid: 1\ndata: hello\n\n"
	got := parseAll(t, strings.NewReader(stream))
	want := []SSEEvent{{Event: "message", ID: "1", Data: "hello"}}
	assertEvents(t, got, want)
}

func TestParseEvents_Comments(t *testing.T) {
	stream := ": keep-alive\n: another comment\n\ndata: x\n\n"
	got := parseAll(t, strings.NewReader(stream))
	want := []SSEEvent{{Data: "x"}}
	assertEvents(t, got, want)
}

func TestParseEvents_EventWithoutDataDoesNotDispatch(t *testing.T) {
	stream := "event: ping\nid: 7\n\ndata: payload\n\n"
	got := parseAll(t, strings.NewReader(stream))
	want := []SSEEvent{{Data: "payload"}}
	assertEvents(t, got, want)
}

func TestParseEvents_PartialReads(t *testing.T) {
	payload := readFixture(t, "sse_partial.txt")

	want := parseAll(t, bytes.NewReader(payload))
	if len(want) == 0 {
		t.Fatal("fixture produced no events")
	}

	got := parseAll(t, &oneByteReader{data: payload})
	assertEvents(t, got, want)
}

func TestParseEvents_DoneSentinel(t *testing.T) {
	got := parseAll(t, strings.NewReader("data: [DONE]\n\n"))
	want := []SSEEvent{{Data: "[DONE]"}}
	assertEvents(t, got, want)
}

func TestParseEvents_CRLF(t *testing.T) {
	stream := "event: message\r\nid: 2\r\ndata: one\r\ndata: two\r\n\r\n"
	got := parseAll(t, strings.NewReader(stream))
	want := []SSEEvent{{Event: "message", ID: "2", Data: "one\ntwo"}}
	assertEvents(t, got, want)
}

func TestParseEvents_FlushesFinalEventWithoutBlankLine(t *testing.T) {
	got := parseAll(t, strings.NewReader("data: tail"))
	want := []SSEEvent{{Data: "tail"}}
	assertEvents(t, got, want)
}

func TestParseEvents_RetryAndUnknownFieldsIgnored(t *testing.T) {
	stream := "retry: 3000\nunknown: value\nid: 9\ndata: ok\n\n"
	got := parseAll(t, strings.NewReader(stream))
	want := []SSEEvent{{ID: "9", Data: "ok"}}
	assertEvents(t, got, want)
}

func TestParseEvents_LongLine(t *testing.T) {
	data := strings.Repeat("a", 100_000)
	stream := "data: " + data + "\n\n"
	got := parseAll(t, strings.NewReader(stream))
	want := []SSEEvent{{Data: data}}
	assertEvents(t, got, want)
}

func TestParseEvents_CallbackErrorStopsParsing(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	err := ParseEvents(strings.NewReader("data: a\n\ndata: b\n\n"), func(SSEEvent) error {
		calls++
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if calls != 1 {
		t.Fatalf("callback called %d times, want 1", calls)
	}
}

func TestParseEvents_InvalidArguments(t *testing.T) {
	if err := ParseEvents(nil, func(SSEEvent) error { return nil }); err == nil {
		t.Fatal("expected error for nil reader")
	}
	if err := ParseEvents(strings.NewReader("data: x\n\n"), nil); !errors.Is(err, ErrNilSSECallback) {
		t.Fatalf("err = %v, want ErrNilSSECallback", err)
	}
}

func TestParseEvents_Concurrent(t *testing.T) {
	payload := readFixture(t, "sse_partial.txt")
	want := parseAll(t, bytes.NewReader(payload))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var got []SSEEvent
			if err := ParseEvents(bytes.NewReader(payload), func(ev SSEEvent) error {
				got = append(got, ev)
				return nil
			}); err != nil {
				t.Errorf("ParseEvents: %v", err)
				return
			}
			if len(got) != len(want) {
				t.Errorf("got %d events, want %d", len(got), len(want))
				return
			}
			for j := range want {
				if got[j] != want[j] {
					t.Errorf("event %d = %#v, want %#v", j, got[j], want[j])
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestWriteSSEEvent_WireFormat(t *testing.T) {
	var buf bytes.Buffer
	n, err := WriteSSEEvent(&buf, SSEEvent{Event: "message", ID: "1", Data: "line1\nline2"})
	if err != nil {
		t.Fatalf("WriteSSEEvent: %v", err)
	}
	const want = "event: message\nid: 1\ndata: line1\ndata: line2\n\n"
	if buf.String() != want {
		t.Fatalf("wire = %q, want %q", buf.String(), want)
	}
	if n != int64(len(want)) {
		t.Fatalf("n = %d, want %d", n, len(want))
	}

	buf.Reset()
	if _, err := WriteSSEEvent(&buf, SSEEvent{Data: ""}); err != nil {
		t.Fatalf("WriteSSEEvent: %v", err)
	}
	if got, want := buf.String(), "data: \n\n"; got != want {
		t.Fatalf("empty data wire = %q, want %q", got, want)
	}
}

func TestWriteSSEEvent_RoundTrip(t *testing.T) {
	input := []byte("event: message\nid: 1\ndata: a\ndata: b\n\ndata: [DONE]\n\n")
	events := parseAll(t, bytes.NewReader(input))
	if len(events) != 2 {
		t.Fatalf("parsed %d events, want 2", len(events))
	}

	var wire bytes.Buffer
	for _, ev := range events {
		if _, err := WriteSSEEvent(&wire, ev); err != nil {
			t.Fatalf("WriteSSEEvent: %v", err)
		}
	}
	got := parseAll(t, bytes.NewReader(wire.Bytes()))
	assertEvents(t, got, events)
	// Multi-line data must survive the round trip as multiple data lines.
	if !strings.Contains(wire.String(), "data: a\ndata: b\n") {
		t.Fatalf("wire lost multi-line data: %q", wire.String())
	}
}

func TestWriteSSEEvent_InvalidArguments(t *testing.T) {
	if _, err := WriteSSEEvent(nil, SSEEvent{Data: "x"}); err == nil {
		t.Fatal("expected error for nil writer")
	}
}

// benchmarkStream builds a realistic 50-event SSE stream of the shape
// produced by OpenAI-compatible chat completion streaming: one JSON delta per
// data line, terminated by the [DONE] sentinel.
func benchmarkStream(n int) []byte {
	var b strings.Builder
	b.Grow(n * 256)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b,
			"data: {\"id\":\"chatcmpl-%d\",\"object\":\"chat.completion.chunk\",\"created\":1700000000,\"model\":\"gpt-4o-mini\",\"system_fingerprint\":\"fp_1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" token-%d \"},\"finish_reason\":null}]}\n\n",
			i, i)
	}
	b.WriteString("data: [DONE]\n\n")
	return []byte(b.String())
}

var benchmarkPayload = benchmarkStream(50)

// BenchmarkSSEParser measures full-stream parse throughput on a realistic
// 51-event stream.
func BenchmarkSSEParser(b *testing.B) {
	b.SetBytes(int64(len(benchmarkPayload)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err := ParseEvents(bytes.NewReader(benchmarkPayload), func(SSEEvent) error { return nil })
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSSEParser_PartialReads measures throughput when the reader hands
// over a single byte per Read, the worst case for buffering overhead.
func BenchmarkSSEParser_PartialReads(b *testing.B) {
	b.SetBytes(int64(len(benchmarkPayload)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err := ParseEvents(&oneByteReader{data: benchmarkPayload}, func(SSEEvent) error { return nil })
		if err != nil {
			b.Fatal(err)
		}
	}
}
