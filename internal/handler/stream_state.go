package handler

import (
	"regexp"
	"strings"
	"sync"

	"github.com/local/redactor/internal/pii"
)

// incompletePlaceholderRe matches a trailing run of text that could be the
// beginning of a placeholder: an opening angle bracket, zero or more uppercase
// letters, and an optional "_" plus digits, with no closing ">".
//
// Examples that match: "<", "<E", "<EMAIL", "<EMAIL_", "<EMAIL_1".
// A complete placeholder ("<EMAIL_1>") does not match because of the ">".
//
// It is deliberately permissive: a match only becomes a buffered prefix when
// splitIncompletePlaceholder's caller confirms it prefixes a known placeholder,
// so a stray "<" in prose is never held back.
var incompletePlaceholderRe = regexp.MustCompile(`<[A-Z]*(?:_[0-9]*)?$`)

// splitIncompletePlaceholder splits text at the last run that could be a partial
// placeholder. It returns (complete, incomplete); incomplete is "" when the text
// does not end with a possible placeholder prefix.
func splitIncompletePlaceholder(text string) (complete, incomplete string) {
	loc := incompletePlaceholderRe.FindStringIndex(text)
	if loc == nil {
		return text, ""
	}
	return text[:loc[0]], text[loc[0]:]
}

// streamState carries per-request buffering state for streamed responses.
//
// Streaming models emit text one token at a time, so a placeholder produced by
// the redactor can arrive split across SSE events: "<EMAIL_1>" might be sent as
// "<", "EMAIL" and "_1>". Unredacting each event in isolation would miss the
// placeholder and leak it to the client. streamState holds the tail of the
// previous event when it is a prefix of a known placeholder, and prepends it to
// the next event before unredacting.
//
// The state is attached to the request context while the request body is
// redacted (see redactBody). That is also where the per-request mapping is
// published, and the proxy carries the same context through to the streaming
// response, so every MutateStreamChunk call for one request sees the same
// *streamState.
//
// A streamState is owned by one request and is safe for concurrent use.
type streamState struct {
	mu    sync.Mutex
	buf   string   // incomplete placeholder held from the previous chunk
	known []string // placeholders in the mapping, loaded once on first use
	ready bool
}

// newStreamState returns an empty streamState.
func newStreamState() *streamState { return &streamState{} }

// unredactLeaf unredacts one selected string leaf, stitching this event's text
// onto any incomplete placeholder held from the previous event. It returns the
// portion that is safe to emit now; a trailing prefix of a known placeholder is
// retained for the next call.
//
// Only prefixes of placeholders that actually exist in the mapping are buffered,
// so ordinary text (including a bare "<" at the end of a chunk) is flushed
// immediately.
func (s *streamState) unredactLeaf(text string, u *pii.Unredactor, m *pii.Mapping) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.ready {
		entries := m.All()
		s.known = make([]string, 0, len(entries))
		for ph := range entries {
			s.known = append(s.known, ph)
		}
		s.ready = true
	}

	combined := s.buf + text
	s.buf = ""

	restored := u.Unredact(combined, m)
	complete, incomplete := splitIncompletePlaceholder(restored)
	if incomplete != "" && s.isPlaceholderPrefix(incomplete) {
		s.buf = incomplete
		return complete
	}
	return restored
}

// isPlaceholderPrefix reports whether incomplete could grow into one of the
// placeholders known for this request.
func (s *streamState) isPlaceholderPrefix(incomplete string) bool {
	for _, ph := range s.known {
		if strings.HasPrefix(ph, incomplete) {
			return true
		}
	}
	return false
}
