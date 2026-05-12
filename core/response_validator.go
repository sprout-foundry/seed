package core

// ResponseValidator inspects LLM response content for quality issues like
// truncation, tentativeness, or other patterns that suggest the response
// should not be finalized yet.
//
// It has zero dependencies on Agent or concrete types — all input is passed
// explicitly and the DebugLog callback is optional.
type ResponseValidator struct {
	debugLog func(format string, args ...interface{})
}

// ResponseValidatorOptions configures a ResponseValidator.
type ResponseValidatorOptions struct {
	// DebugLog is an optional callback for debug output. When nil,
	// debug logging is disabled.
	DebugLog func(format string, args ...interface{})
}

// NewResponseValidator creates a new ResponseValidator with the given options.
func NewResponseValidator(opts ResponseValidatorOptions) *ResponseValidator {
	return &ResponseValidator{
		debugLog: opts.DebugLog,
	}
}
