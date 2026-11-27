package detectors

import (
	"reflect"
	"testing"

	"github.com/local/redactor/internal/pii/core"
)

func TestCredentialDetector(t *testing.T) {
	d := NewCredential()
	if got, want := d.Category(), core.CategoryCredential; got != want {
		t.Fatalf("Category() = %q, want %q", got, want)
	}

	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "url_userinfo",
			text: "https://admin:s3cret@api.example.com/v1",
			want: []string{"admin:s3cret"},
		},
		{
			name: "url_userinfo_without_scheme",
			text: "admin:s3cret@api.example.com",
			want: []string{"admin:s3cret"},
		},
		{
			name: "query_string",
			text: "?password=hunter2&user=me",
			want: []string{"hunter2"},
		},
		{
			name: "form_data",
			text: "user=me&passwd=letmein",
			want: []string{"letmein"},
		},
		{
			name: "json_password",
			text: `{"password": "s3cret"}`,
			want: []string{"s3cret"},
		},
		{
			name: "json_token",
			text: `{"token": "ghp_xxxx"}`,
			want: []string{"ghp_xxxx"},
		},
		{
			name: "json_api_key",
			text: `{"apikey": "abc123"}`,
			want: []string{"abc123"},
		},
		{
			name: "yaml_password",
			text: "password: hunter2",
			want: []string{"hunter2"},
		},
		{
			name: "yaml_token",
			text: "token: ghp_yyyy",
			want: []string{"ghp_yyyy"},
		},
		{
			name: "standalone",
			text: "credentials are admin:s3cret here",
			want: []string{"admin:s3cret"},
		},
		{
			name: "api_key_query",
			text: "api_key=sk-proj-abc123",
			want: []string{"sk-proj-abc123"},
		},
		{
			name: "multiple",
			text: "password=one token: two",
			want: []string{"one", "two"},
		},
		{
			name: "negative_plain_text",
			text: "the quarterly report looks fine",
			want: nil,
		},
		{
			name: "negative_bare_word",
			text: "password123",
			want: nil,
		},
		{
			name: "negative_url_without_userinfo",
			text: "https://api.example.com/v1",
			want: nil,
		},
		{
			name: "negative_host_port",
			text: "https://api.example.com:28443/v1",
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matches := d.Detect(tc.text)

			var got []string
			for i, m := range matches {
				if m.Category != core.CategoryCredential {
					t.Fatalf("match %d category = %q, want %q", i, m.Category, core.CategoryCredential)
				}
				if m.Start < 0 || m.End > len(tc.text) || m.Start >= m.End {
					t.Fatalf("match %d has invalid span [%d:%d] for %q", i, m.Start, m.End, tc.text)
				}
				if want := tc.text[m.Start:m.End]; m.Value != want {
					t.Fatalf("match %d value = %q, want %q", i, m.Value, want)
				}
				got = append(got, m.Value)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Detect(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}
