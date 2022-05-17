package errors

import "fmt"

type AggregateError struct {
	Errors []error
}

func (e *AggregateError) Error() string {
	if e == nil {
		return fmt.Sprintf("AggregateError: nil")
	}
	return fmt.Sprintf("AggregateError: %v", e.Errors)
}

// AggregateErrorFromChannel gathers non-nil error values (if any)
// from the given channel and bundles them into an AggregateError.
// The channel must contain some finite number of errors and be closed.
// If no errors are read from the channel, nil is returned.
func AggregateErrorFromChannel(errorchan <-chan error) error {
	errs := make([]error, 0)
	for err := range errorchan {
		if err == nil {
			continue
		}
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return &AggregateError{Errors: errs}
	}
	return nil
}
