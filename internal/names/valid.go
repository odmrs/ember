package names

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxBytes is KOReader's own margin under FAT's 255 UTF-16 units.
const MaxBytes = 240

// Why Valid refused a name.
const (
	ReasonEmpty    = "empty"
	ReasonDots     = "dots"     // "." or ".."
	ReasonEncoding = "encoding" // not UTF-8
	ReasonReserved = "reserved" // one of \ / : * ? " < > |
	ReasonControl  = "control"
	ReasonTrailing = "trailing" // ends in a dot or a space
	ReasonTooLong  = "too_long"
	ReasonHidden   = "hidden"  // starts with a dot, so nothing would list it
	ReasonSidecar  = "sidecar" // ends in .sdr, which KOReader owns
)

// ErrInvalidName matches every *NameError.
var ErrInvalidName = errors.New("names: invalid name")

// NameError is a name Valid refused.
type NameError struct {
	Name   string
	Reason string
}

func (e *NameError) Error() string {
	return fmt.Sprintf("names: invalid name %q: %s", e.Name, e.Reason)
}

func (e *NameError) Is(target error) bool { return target == ErrInvalidName }

// Valid checks a new name for a Folder or a Book. The Library sits on
// FAT, so it refuses what FAT cannot hold, and names the Library listing
// would hide. Refusing hidden names also keeps every name clear of the
// parts uploads write to, which start with a dot.
func Valid(name string) error {
	var reason string
	switch {
	case name == "":
		reason = ReasonEmpty
	case name == "." || name == "..":
		reason = ReasonDots
	case !utf8.ValidString(name):
		reason = ReasonEncoding
	case strings.ContainsAny(name, `\/:*?"<>|`):
		reason = ReasonReserved
	case strings.ContainsFunc(name, unicode.IsControl):
		reason = ReasonControl
	case strings.HasSuffix(name, ".") || strings.HasSuffix(name, " "):
		reason = ReasonTrailing
	case len(name) > MaxBytes:
		reason = ReasonTooLong
	case strings.HasPrefix(name, "."):
		reason = ReasonHidden
	case strings.HasSuffix(strings.ToLower(name), ".sdr"):
		reason = ReasonSidecar
	default:
		return nil
	}
	return &NameError{Name: name, Reason: reason}
}
