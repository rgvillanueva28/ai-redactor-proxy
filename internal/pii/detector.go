// Package pii implements PII detection, redaction and unredaction for proxied
// AI requests and responses.
package pii

// Category enumerates supported PII categories.
type Category string

const (
	CategoryEmail      Category = "EMAIL"
	CategoryPhone      Category = "PHONE"
	CategoryCreditCard Category = "CC"
	CategorySSN        Category = "SSN"
	CategoryIP         Category = "IP"
	CategoryPath       Category = "PATH"
	CategoryAPIKey     Category = "APIKEY"
	CategoryDomain     Category = "DOMAIN"
	CategoryClientName Category = "CLIENT"
	CategoryPersonName Category = "NAME"
)

// Match describes a single PII occurrence.
type Match struct {
	Start    int      // byte offset inclusive
	End      int      // byte offset exclusive
	Category Category // category of the matched text
	Value    string   // original text (== input[Start:End], but kept for clarity)
}

// Detector identifies PII in a string.
type Detector interface {
	Category() Category
	Detect(text string) []Match
}
