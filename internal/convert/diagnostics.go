// Stable diagnostic codes defined by Blueprint+ §15.
//
// Drafter emits its own numeric codes for parser problems; this catalogue
// is used by the *converter* (post-parse stage) so authors get
// predictable, documentable codes for our checks regardless of which
// drafter version is bundled.
package convert

// Stable diagnostic codes - keep in sync with specs/apib+-converter.md §8.
const (
	// Errors.
	CodeInvalidHTTPMethod  = "E001"
	CodeMalformedURI       = "E002"
	CodeMalformedLocation  = "E003"
	CodeUnknownLocation    = "E004"
	CodeDuplicateOperation = "E005"
	CodeUndefinedType      = "E006"
	CodeMissingSchemeField = "E007"
	CodeUnknownSchemeType  = "E008"

	// Warnings.
	CodeUnknownMetadataKey = "W001"
	CodeMultipleVersion    = "W002"
	CodeWebhookOnOAS30     = "W003"
	CodeImplicitDefault    = "W005"
	CodeDeprecatedSyntax   = "W006"
	CodeUndefinedScheme    = "W007"
)

// Diagnostics collects converter-emitted Annotations during a single
// translation pass. Pass via Options.Diagnostics to receive them; nil
// is the no-op default for callers that don't care.
type Diagnostics struct {
	Items []Annotation
}

// NewDiagnostics returns an empty, ready-to-use collector.
func NewDiagnostics() *Diagnostics { return &Diagnostics{} }

// Warn appends a warning-severity Annotation with the given stable code.
func (d *Diagnostics) Warn(code, message string) {
	if d == nil {
		return
	}
	d.Items = append(d.Items, Annotation{Severity: "warning", StableCode: code, Message: message})
}

// Error appends an error-severity Annotation with the given stable code.
func (d *Diagnostics) Error(code, message string) {
	if d == nil {
		return
	}
	d.Items = append(d.Items, Annotation{Severity: "error", StableCode: code, Message: message})
}

// HasErrors reports whether any collected Annotation is an error.
func (d *Diagnostics) HasErrors() bool {
	if d == nil {
		return false
	}
	for _, a := range d.Items {
		if a.Severity == "error" {
			return true
		}
	}
	return false
}
