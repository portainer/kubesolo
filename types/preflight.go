package types

// PreflightReport is what `kubesoloctl check --json` prints: the result of
// every pre-flight check, in the order they ran.
type PreflightReport struct {
	Checks []PreflightCheck `json:"checks"`
}

// PreflightCheck is the result of one pre-flight check.
type PreflightCheck struct {
	Name   string          `json:"name"`
	Result PreflightResult `json:"result"`
	// Message says what the check found; empty when it passed.
	Message string `json:"message,omitempty"`
}

// PreflightResult is the outcome of a pre-flight check. Only a failure stops
// an install.
type PreflightResult string

const (
	PreflightPass PreflightResult = "pass"
	PreflightWarn PreflightResult = "warn"
	PreflightFail PreflightResult = "fail"
)

// Failed reports whether any check failed.
func (r PreflightReport) Failed() bool {
	for _, c := range r.Checks {
		if c.Result == PreflightFail {
			return true
		}
	}
	return false
}
