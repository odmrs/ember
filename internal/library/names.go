package library

import "github.com/odmrs/ember/internal/names"

// maxNameBytes is KOReader's own margin under FAT's 255 UTF-16 units.
const maxNameBytes = names.MaxBytes

// Why ValidName refused a name.
const (
	ReasonEmpty    = names.ReasonEmpty
	ReasonDots     = names.ReasonDots
	ReasonEncoding = names.ReasonEncoding
	ReasonReserved = names.ReasonReserved
	ReasonControl  = names.ReasonControl
	ReasonTrailing = names.ReasonTrailing
	ReasonTooLong  = names.ReasonTooLong
	ReasonHidden   = names.ReasonHidden
	ReasonSidecar  = names.ReasonSidecar
)

// ErrInvalidName matches every *NameError.
var ErrInvalidName = names.ErrInvalidName

// NameError is a name ValidName refused.
type NameError = names.NameError

// ValidName checks a new name for a Folder or a Book; see names.Valid.
func ValidName(name string) error { return names.Valid(name) }
