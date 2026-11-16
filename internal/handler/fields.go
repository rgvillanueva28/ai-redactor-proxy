package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// fieldSet is the set of object-key paths whose string leaves a provider
// handler rewrites, encoded as NUL separated keys.
//
// Array elements are transparent: the path "messages.content" selects both
//
//	{"messages":[{"content":"hi"}]}
//
// and
//
//	{"messages":[{"content":[{"type":"text","text":"hi"}]}]}
//
// via the additional path "messages.content.text". Only the leaves named by a
// path are touched, so fields that merely happen to contain PII (tool
// definitions, image URLs, metadata) survive untouched — the upstream API
// expects them verbatim.
type fieldSet map[string]struct{}

// pathSep separates the object keys in an encoded field path. A NUL byte
// cannot appear in a JSON object key in practice, so the encoding is
// unambiguous.
const pathSep = "\x00"

// newFieldSet builds a fieldSet from the given key paths.
func newFieldSet(paths ...[]string) fieldSet {
	set := make(fieldSet, len(paths))
	for _, p := range paths {
		set[strings.Join(p, pathSep)] = struct{}{}
	}
	return set
}

// contains reports whether path is one of the selected leaves.
func (f fieldSet) contains(path []string) bool {
	if len(f) == 0 {
		return false
	}
	_, ok := f[strings.Join(path, pathSep)]
	return ok
}

// walkFields parses src as JSON, applies cb to every string leaf selected by
// fields, and returns the re-serialized JSON.
//
// It mirrors WalkJSON: object key order, numeric literals (UseNumber) and the
// overall document shape are preserved, and strings are written without HTML
// escaping so placeholders like "<EMAIL_1>" stay readable. Whitespace is not
// preserved. Callers that need byte-for-byte pass-through compare the result
// against the input and return the input when nothing changed.
func walkFields(src []byte, fields fieldSet, cb StringCallback) ([]byte, error) {
	if cb == nil {
		cb = identityString
	}

	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()

	var out bytes.Buffer
	out.Grow(len(src))

	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)

	w := &fieldWalker{dec: dec, out: &out, enc: enc, cb: cb, fields: fields}

	tok, err := dec.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("handler: walkFields: empty input")
		}
		return nil, fmt.Errorf("handler: walkFields: %w", err)
	}
	if err := w.value(tok); err != nil {
		return nil, fmt.Errorf("handler: walkFields: %w", err)
	}

	// Reject trailing content so a malformed body is never silently truncated.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("handler: walkFields: unexpected trailing JSON value")
		}
		return nil, fmt.Errorf("handler: walkFields: %w", err)
	}

	return out.Bytes(), nil
}

// fieldWalker holds the state shared by the recursive descent, including the
// object-key path of the value currently being written.
type fieldWalker struct {
	dec    *json.Decoder
	out    *bytes.Buffer
	enc    *json.Encoder
	cb     StringCallback
	fields fieldSet
	path   []string
}

// value writes tok and, for a container, everything nested in it.
func (w *fieldWalker) value(tok json.Token) error {
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			return w.object()
		case '[':
			return w.array()
		default:
			return fmt.Errorf("unexpected delimiter %q", v)
		}
	case string:
		if !w.fields.contains(w.path) {
			return w.writeString(v)
		}
		s, err := w.cb(v)
		if err != nil {
			return err
		}
		return w.writeString(s)
	case json.Number:
		w.out.WriteString(v.String())
		return nil
	case bool:
		w.out.WriteString(strconv.FormatBool(v))
		return nil
	case nil:
		w.out.WriteString("null")
		return nil
	default:
		return fmt.Errorf("unexpected JSON token %v", tok)
	}
}

// object walks an object whose opening '{' has already been consumed. Each
// key is pushed onto the path for the duration of its value, so nested leaves
// see the full key path.
func (w *fieldWalker) object() error {
	w.out.WriteByte('{')
	first := true
	for {
		tok, err := w.dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '}' {
				break
			}
			return fmt.Errorf("unexpected delimiter %q in object", d)
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("unexpected object key token %v", tok)
		}
		if !first {
			w.out.WriteByte(',')
		}
		first = false
		if err := w.writeString(key); err != nil {
			return err
		}
		w.out.WriteByte(':')

		val, err := w.dec.Token()
		if err != nil {
			return err
		}
		w.path = append(w.path, key)
		err = w.value(val)
		w.path = w.path[:len(w.path)-1]
		if err != nil {
			return err
		}
	}
	w.out.WriteByte('}')
	return nil
}

// array walks an array whose opening '[' has already been consumed. Elements
// inherit the enclosing path: arrays never add a segment, which is what makes
// messages[].content addressable as a single path.
func (w *fieldWalker) array() error {
	w.out.WriteByte('[')
	first := true
	for {
		tok, err := w.dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok && d == ']' {
			break
		}
		if !first {
			w.out.WriteByte(',')
		}
		first = false
		if err := w.value(tok); err != nil {
			return err
		}
	}
	w.out.WriteByte(']')
	return nil
}

// writeString encodes s as a JSON string without HTML escaping. Encode appends
// a newline, which is trimmed so the surrounding output stays compact.
func (w *fieldWalker) writeString(s string) error {
	if err := w.enc.Encode(s); err != nil {
		return err
	}
	w.out.Truncate(w.out.Len() - 1)
	return nil
}
