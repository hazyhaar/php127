package stdlib

import "code.hazyhaar.fr/devhoros/php127/types"

type TypeError struct{ text string }
type ValueError struct{ text string }
type ArithmeticError struct{ text string }
type ArgumentCountError struct{ text string }
type Error struct{ text string }
type JsonException struct{ text string }

func (e TypeError) Error() string          { return e.text }
func (e ValueError) Error() string         { return e.text }
func (e ArithmeticError) Error() string    { return e.text }
func (e ArgumentCountError) Error() string { return e.text }
func (e Error) Error() string              { return e.text }
func (e JsonException) Error() string      { return e.text }

func ExceptionClass(err error) string {
	if err == nil {
		return ""
	}
	switch err.(type) {
	case TypeError:
		return "TypeError"
	case ValueError:
		return "ValueError"
	case ArithmeticError:
		return "ArithmeticError"
	case ArgumentCountError:
		return "ArgumentCountError"
	case Error:
		return "Error"
	case JsonException:
		return "JsonException"
	case types.DivisionByZeroError:
		return "DivisionByZeroError"
	default:
		return ""
	}
}

func argWord(n int) string {
	if n == 1 {
		return "argument"
	}
	return "arguments"
}

func arity(name string, n, minN, maxN int) error {
	if n < minN {
		if minN == maxN {
			return ArgumentCountError{sprintfCount(name, "exactly", minN, n)}
		}
		return ArgumentCountError{sprintfCount(name, "at least", minN, n)}
	}
	if maxN >= 0 && n > maxN {
		return ArgumentCountError{sprintfCount(name, "at most", maxN, n)}
	}
	return nil
}

func sprintfCount(name, how string, want, got int) string {
	return name + "() expects " + how + " " + itoa(want) + " " + argWord(want) + ", " + itoa(got) + " given"
}

func typeErr(fn, idx, param, want, got string) TypeError {
	return TypeError{fn + "(): Argument #" + idx + " ($" + param + ") must be of type " + want + ", " + got + " given"}
}
