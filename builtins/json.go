// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/builtins: Encodeur et décodeur JSON PHP 8.4

package builtins

import (
	"encoding/json"
	"fmt"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
)

// JsonEncode convertit une types.Value en chaîne JSON
func JsonEncode(v types.Value) types.Value {
	var sb strings.Builder
	encodeValue(v, &sb)
	return types.NewString(sb.String())
}

func encodeValue(v types.Value, sb *strings.Builder) {
	switch v.Type {
	case types.TypeNull, types.TypeUndef:
		sb.WriteString("null")
	case types.TypeTrue:
		sb.WriteString("true")
	case types.TypeFalse:
		sb.WriteString("false")
	case types.TypeLong:
		sb.WriteString(fmt.Sprintf("%d", v.ToLong()))
	case types.TypeDouble:
		sb.WriteString(fmt.Sprintf("%g", v.ToDouble()))
	case types.TypeString:
		b, _ := json.Marshal(v.String())
		sb.Write(b)
	default:
		sb.WriteString("null")
	}
}

// JsonDecode décode un flux JSON vers une types.Value
func JsonDecode(v types.Value, assoc bool) types.Value {
	str := v.String()
	var raw interface{}
	if err := json.Unmarshal([]byte(str), &raw); err != nil {
		return types.NewNull()
	}
	return convertGoToPHPValue(raw)
}

func convertGoToPHPValue(raw interface{}) types.Value {
	if raw == nil {
		return types.NewNull()
	}
	switch val := raw.(type) {
	case bool:
		return types.NewBool(val)
	case float64:
		if val == float64(int64(val)) {
			return types.NewLong(int64(val))
		}
		return types.NewDouble(val)
	case string:
		return types.NewString(val)
	default:
		return types.NewNull()
	}
}
