// Package pii implements PII detection, redaction and unredaction for proxied
// AI requests and responses.
package pii

import "github.com/local/redactor/internal/pii/core"

// The detector contract lives in package core so that the concrete detectors
// (package detectors, imported by the registry) can depend on it without
// creating an import cycle with this package. These aliases keep pii.Category,
// pii.Match and pii.Detector as the public contract used everywhere else.

// Category enumerates supported PII categories.
type Category = core.Category

// Match describes a single PII occurrence.
type Match = core.Match

// Detector identifies PII in a string.
type Detector = core.Detector

const (
	CategoryEmail      = core.CategoryEmail
	CategoryPhone      = core.CategoryPhone
	CategoryCreditCard = core.CategoryCreditCard
	CategorySSN        = core.CategorySSN
	CategoryIP         = core.CategoryIP
	CategoryPath       = core.CategoryPath
	CategoryAPIKey     = core.CategoryAPIKey
	CategoryCredential = core.CategoryCredential
	CategoryDomain     = core.CategoryDomain
	CategoryClientName = core.CategoryClientName
	CategoryPersonName = core.CategoryPersonName
)
