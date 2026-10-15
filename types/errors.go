package types

// DivisionByZeroError represents PHP's division-by-zero exception.
// The empty value implements error without requiring a heap allocation.
type DivisionByZeroError struct{}

func (DivisionByZeroError) Error() string { return "Division by zero" }
