// Package core defines the detector contract shared by package pii and the
// concrete detectors in package detectors. It lives in its own package so that
// pii can wire detectors into its registry without an import cycle (pii ->
// detectors -> core).
package core

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
	CategoryCredential Category = "CRED"
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
