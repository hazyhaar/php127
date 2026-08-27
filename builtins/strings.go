// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/builtins: Primitives de manipulation de chaînes PHP 8.4

package builtins

import (
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
)

func Trim(strVal, maskVal types.Value) types.Value {
	s := strVal.String()
	cutset := " \t\n\r\000\x0B"
	if maskVal.Type == types.TypeString {
		cutset = maskVal.String()
	}
	return types.NewString(strings.Trim(s, cutset))
}

func Ltrim(strVal, maskVal types.Value) types.Value {
	s := strVal.String()
	cutset := " \t\n\r\000\x0B"
	if maskVal.Type == types.TypeString {
		cutset = maskVal.String()
	}
	return types.NewString(strings.TrimLeft(s, cutset))
}

func Rtrim(strVal, maskVal types.Value) types.Value {
	s := strVal.String()
	cutset := " \t\n\r\000\x0B"
	if maskVal.Type == types.TypeString {
		cutset = maskVal.String()
	}
	return types.NewString(strings.TrimRight(s, cutset))
}

func Strtolower(strVal types.Value) types.Value {
	return types.NewString(strings.ToLower(strVal.String()))
}

func Strtoupper(strVal types.Value) types.Value {
	return types.NewString(strings.ToUpper(strVal.String()))
}

func StrReplace(searchVal, replaceVal, subjectVal types.Value) types.Value {
	search := searchVal.String()
	replace := replaceVal.String()
	subject := subjectVal.String()
	return types.NewString(strings.ReplaceAll(subject, search, replace))
}

func StrRepeat(inputVal, multVal types.Value) types.Value {
	s := inputVal.String()
	n := int(multVal.ToLong())
	if n <= 0 {
		return types.NewString("")
	}
	return types.NewString(strings.Repeat(s, n))
}
