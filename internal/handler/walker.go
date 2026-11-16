// Package handler implements the request/response mutators that redact PII
// from AI CLI traffic before it is forwarded upstream and restore it in the
// response that comes back.
package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// StringCallback is called for every string leaf in the JSON.
//
// It receives the decoded string value and returns the replacement. Returning
// an error aborts the walk; no output is produced in that case.
type StringCallback func(s string) (string, error)

// WalkJSON parses src as JSON, applies cb to every string leaf, and returns the
// re-serialized JSON.
//
// Object key order is preserved: the input is walked with json.Decoder.Token,
// which reports tokens in document order, and the output is written token by
// token. Object *keys* are never passed to cb — redacting them would change the
// shape of the document the upstream API expects.
//
// Numeric precision is preserved: the decoder runs with UseNumber, so numbers
// are copied through as their original literals instead of being round-tripped
// through float64. That matters for large integers (snowflake-style request
// ids, nanosecond timestamps) and for high-precision decimals.
//
// Strings are re-encoded with html escaping disabled, so a placeholder such as
// "<EMAIL_1>" survives as-is rather than becoming "\u003cEMAIL_1\u003e".
//
// Whitespace in the input is not preserved; the output is compact. JSON does
// not give whitespace meaning, so this is invisible to a conforming parser.
func WalkJSON(src []byte, cb StringCallback) ([]byte, error) {
	if cb == nil {
		cb = identityString
	}

	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()

	var out bytes.Buffer
	out.Grow(len(src))

	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)

	w := jsonWalker{dec: dec, out: &out, enc: enc, cb: cb}

	tok, err := dec.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("handler: WalkJSON: empty input")
		}
		return nil, fmt.Errorf("handler: WalkJSON: %w", err)
	}
	if err := w.value(tok); err != nil {
		return nil, fmt.Errorf("handler: WalkJSON: %w", err)
	}

	// Reject trailing content ("{} {}") so a malformed body is never silently
	// truncated and forwarded.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("handler: WalkJSON: unexpected trailing JSON value")
		}
		return nil, fmt.Errorf("handler: WalkJSON: %w", err)
	}

	return out.Bytes(), nil
}

// identityString is the default StringCallback.
func identityString(s string) (string, error) { return s, nil }

// jsonWalker holds the state shared by the recursive descent.
type jsonWalker struct {
	dec *json.Decoder
	out *bytes.Buffer
	enc *json.Encoder
	cb  StringCallback
}

// value writes the token tok and, for a container, everything nested in it.
func (w *jsonWalker) value(tok json.Token) error {
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

// object walks an object whose opening '{' has already been consumed. Token
// hides ',' and ':'; in object context it yields each key followed by its
// value, then the closing '}'.
func (w *jsonWalker) object() error {
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
		if err := w.value(val); err != nil {
			return err
		}
	}
	w.out.WriteByte('}')
	return nil
}

// array walks an array whose opening '[' has already been consumed.
func (w *jsonWalker) array() error {
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
		// A nested container (delimiter '{' or '[') is an ordinary element.
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
func (w *jsonWalker) writeString(s string) error {
	if err := w.enc.Encode(s); err != nil {
		return err
	}
	w.out.Truncate(w.out.Len() - 1)
	return nil
}
