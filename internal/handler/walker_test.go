package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// upper is the common callback used by the walker tests.
func upper(s string) (string, error) { return strings.ToUpper(s), nil }

func TestWalkJSON_SimpleObject(t *testing.T) {
	src := []byte(`{"name":"alice","city":"paris"}`)
	got, err := WalkJSON(src, upper)
	if err != nil {
		t.Fatalf("WalkJSON: %v", err)
	}
	if want := `{"name":"ALICE","city":"PARIS"}`; string(got) != want {
		t.Fatalf("WalkJSON = %s, want %s", got, want)
	}
}

func TestWalkJSON_NestedObject(t *testing.T) {
	// Keys must stay untouched while every nested string value is transformed.
	src := []byte(`{"a":"x","b":{"c":["y",{"d":"z"}],"e":[]},"f":{"g":"w"}}`)
	got, err := WalkJSON(src, upper)
	if err != nil {
		t.Fatalf("WalkJSON: %v", err)
	}
	want := `{"a":"X","b":{"c":["Y",{"d":"Z"}],"e":[]},"f":{"g":"W"}}`
	if string(got) != want {
		t.Fatalf("WalkJSON = %s, want %s", got, want)
	}
}

func TestWalkJSON_PreservesNumbers(t *testing.T) {
	// None of these survive a float64 round trip: the big integer exceeds 2^53,
	// the decimals are not exactly representable, and 1e400 overflows float64.
	src := []byte(`{"big":123456789012345678901234567890,"float":0.30000000000000004,"exp":1e400,"neg":-2.5e-10,"zero":0}`)
	got, err := WalkJSON(src, nil)
	if err != nil {
		t.Fatalf("WalkJSON: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("WalkJSON = %s, want %s", got, src)
	}

	// Belt and braces: decoding with UseNumber must yield the same literals.
	var m map[string]json.Number
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal numbers: %v", err)
	}
	for key, want := range map[string]string{
		"big":   "123456789012345678901234567890",
		"float": "0.30000000000000004",
		"exp":   "1e400",
		"neg":   "-2.5e-10",
		"zero":  "0",
	} {
		if m[key].String() != want {
			t.Errorf("%s = %q, want %q", key, m[key].String(), want)
		}
	}
}

func TestWalkJSON_PreservesBooleansAndNull(t *testing.T) {
	src := []byte(`{"t":true,"f":false,"n":null,"arr":[true,false,null],"o":{"n":null}}`)
	got, err := WalkJSON(src, nil)
	if err != nil {
		t.Fatalf("WalkJSON: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("WalkJSON = %s, want %s", got, src)
	}
}

func TestWalkJSON_EmptyObjectAndArray(t *testing.T) {
	for _, src := range []string{`{}`, `[]`, `{"a":{},"b":[]}`, `[[],{}]`} {
		got, err := WalkJSON([]byte(src), upper)
		if err != nil {
			t.Fatalf("WalkJSON(%s): %v", src, err)
		}
		if string(got) != src {
			t.Errorf("WalkJSON(%s) = %s, want %s", src, got, src)
		}
	}
}

func TestWalkJSON_TopLevelScalars(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{`"plain string"`, `"PLAIN STRING"`},
		{`42`, `42`},
		{`1.50`, `1.50`},
		{`true`, `true`},
		{`false`, `false`},
		{`null`, `null`},
	}
	for _, tt := range tests {
		got, err := WalkJSON([]byte(tt.src), upper)
		if err != nil {
			t.Fatalf("WalkJSON(%s): %v", tt.src, err)
		}
		if string(got) != tt.want {
			t.Errorf("WalkJSON(%s) = %s, want %s", tt.src, got, tt.want)
		}
	}
}

func TestWalkJSON_CallbackError(t *testing.T) {
	sentinel := errors.New("callback exploded")
	src := []byte(`{"a":"fine","b":"boom","c":"also fine"}`)

	got, err := WalkJSON(src, func(s string) (string, error) {
		if s == "boom" {
			return "", sentinel
		}
		return s, nil
	})
	if err == nil {
		t.Fatal("WalkJSON returned nil error")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap the callback error", err)
	}
	if got != nil {
		t.Fatalf("WalkJSON returned partial output %q, want nil", got)
	}
}

func TestWalkJSON_InvalidJSON(t *testing.T) {
	cases := map[string]string{
		"empty":                               "",
		"truncated":                           `{"a":`,
		"trailing value":                      `{"a":1} {"b":2}`,
		"trailing junk":                       `{"a":1} junk`,
		"bad array":                           `[1,]`,
		"not json":                            `nope`,
		"duplicate key ok but non-string key": `{1:"a"}`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := WalkJSON([]byte(src), nil)
			if err == nil {
				t.Fatalf("WalkJSON(%q) = %s, want error", src, got)
			}
			if got != nil {
				t.Fatalf("WalkJSON(%q) returned output %q alongside error", src, got)
			}
		})
	}
}

func TestWalkJSON_PlaceholderSafe(t *testing.T) {
	src := []byte(`{"text":"<EMAIL_1>","html":"a<b>c&d","multi":"<PHONE_2> and <CC_10>"}`)
	got, err := WalkJSON(src, nil)
	if err != nil {
		t.Fatalf("WalkJSON: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("WalkJSON = %s, want %s", got, src)
	}
	if bytes.Contains(got, []byte(`\u003c`)) || bytes.Contains(got, []byte(`\u0026`)) {
		t.Fatalf("output HTML-escaped placeholders: %s", got)
	}
}

func TestWalkJSON_NilCallbackIsIdentity(t *testing.T) {
	src := []byte(`{"a":"b","c":[1,2,{"d":null}]}`)
	got, err := WalkJSON(src, nil)
	if err != nil {
		t.Fatalf("WalkJSON: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("WalkJSON = %s, want %s", got, src)
	}
}

func TestWalkJSON_CallbackOutputIsEscapedCorrectly(t *testing.T) {
	// The rebuilt document must stay parseable when a replacement contains
	// quotes, backslashes or newlines.
	cb := func(string) (string, error) { return "he said \"hi\"\n\\done", nil }
	src := []byte(`{"msg":"original"}`)

	got, err := WalkJSON(src, cb)
	if err != nil {
		t.Fatalf("WalkJSON: %v", err)
	}
	var decoded struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%s)", err, got)
	}
	if want := "he said \"hi\"\n\\done"; decoded.Msg != want {
		t.Fatalf("decoded msg = %q, want %q", decoded.Msg, want)
	}
}

func TestWalkJSON_PreservesKeyOrder(t *testing.T) {
	src := []byte(`{"z":"1","a":"2","m":"3"}`)
	got, err := WalkJSON(src, nil)
	if err != nil {
		t.Fatalf("WalkJSON: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("WalkJSON reordered keys: %s, want %s", got, src)
	}
}

// TestWalkFields_Subtree covers the subtree selector: a path registered as a
// subtree has every string leaf beneath it walked, at any depth, while object
// keys and unrelated paths are left alone.
func TestWalkFields_Subtree(t *testing.T) {
	fields := mergeFieldSets(
		newFieldSet([]string{"keep"}),
		newSubtreeFieldSet([]string{"tool", "input"}),
	)

	t.Run("nested objects", func(t *testing.T) {
		src := []byte(`{"tool":{"input":{"cmd":"alice@example.com","opts":{"user":"bob@x.com"}}},"keep":"alice@example.com","other":"carol@x.com"}`)
		want := `{"tool":{"input":{"cmd":"ALICE@EXAMPLE.COM","opts":{"user":"BOB@X.COM"}}},"keep":"ALICE@EXAMPLE.COM","other":"carol@x.com"}`
		got, err := walkFields(src, fields, upper)
		if err != nil {
			t.Fatalf("walkFields: %v", err)
		}
		if string(got) != want {
			t.Fatalf("walkFields = %s, want %s", got, want)
		}
	})

	t.Run("arrays inside subtree", func(t *testing.T) {
		src := []byte(`{"tool":{"input":[{"text":"alice@example.com"},{"text":"bob@x.com"}]},"other":"alice@example.com"}`)
		want := `{"tool":{"input":[{"text":"ALICE@EXAMPLE.COM"},{"text":"BOB@X.COM"}]},"other":"alice@example.com"}`
		got, err := walkFields(src, fields, upper)
		if err != nil {
			t.Fatalf("walkFields: %v", err)
		}
		if string(got) != want {
			t.Fatalf("walkFields = %s, want %s", got, want)
		}
	})

	t.Run("object keys are never rewritten", func(t *testing.T) {
		src := []byte(`{"tool":{"input":{"alice@example.com":"value"}}}`)
		want := `{"tool":{"input":{"alice@example.com":"VALUE"}}}`
		got, err := walkFields(src, fields, upper)
		if err != nil {
			t.Fatalf("walkFields: %v", err)
		}
		if string(got) != want {
			t.Fatalf("walkFields = %s, want %s", got, want)
		}
	})

	t.Run("subtree root that is not a container selects nothing", func(t *testing.T) {
		// Only containers enter the subtree: a scalar at the root path is
		// matched by an exact path or not at all.
		src := []byte(`{"tool":{"input":"alice@example.com"}}`)
		got, err := walkFields(src, fields, upper)
		if err != nil {
			t.Fatalf("walkFields: %v", err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("walkFields = %s, want %s", got, src)
		}
	})

	t.Run("empty subtree set selects nothing", func(t *testing.T) {
		empty := newSubtreeFieldSet()
		if !empty.isEmpty() {
			t.Fatal("newSubtreeFieldSet() is not empty")
		}
		src := []byte(`{"a":"x"}`)
		got, err := walkFields(src, empty, upper)
		if err != nil {
			t.Fatalf("walkFields: %v", err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("walkFields = %s, want %s", got, src)
		}
	})
}

func TestWalkJSON_DeeplyNestedArray(t *testing.T) {
	src := []byte(`[[["deep"]]]`)
	got, err := WalkJSON(src, upper)
	if err != nil {
		t.Fatalf("WalkJSON: %v", err)
	}
	if want := `[[["DEEP"]]]`; string(got) != want {
		t.Fatalf("WalkJSON = %s, want %s", got, want)
	}
}
