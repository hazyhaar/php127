// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/builtins: Primitives Date et Horodatage PHP 8.4

package builtins

import (
	"fmt"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
)

func Time() types.Value {
	return types.NewLong(time.Now().Unix())
}

func Microtime(asFloat bool) types.Value {
	now := time.Now()
	if asFloat {
		sec := float64(now.Unix())
		usec := float64(now.Nanosecond()) / 1e9
		return types.NewDouble(sec + usec)
	}
	return types.NewString(fmt.Sprintf("%0.8f %d", float64(now.Nanosecond())/1e9, now.Unix()))
}

func Date(formatVal types.Value, tsVal types.Value) types.Value {
	format := formatVal.String()
	var t time.Time
	if tsVal.Type == types.TypeNull || tsVal.Type == types.TypeUndef {
		t = time.Now()
	} else {
		t = time.Unix(tsVal.ToLong(), 0)
	}

	var res []byte
	for i := 0; i < len(format); i++ {
		ch := format[i]
		switch ch {
		case 'Y':
			res = append(res, []byte(fmt.Sprintf("%04d", t.Year()))...)
		case 'y':
			res = append(res, []byte(fmt.Sprintf("%02d", t.Year()%100))...)
		case 'm':
			res = append(res, []byte(fmt.Sprintf("%02d", t.Month()))...)
		case 'd':
			res = append(res, []byte(fmt.Sprintf("%02d", t.Day()))...)
		case 'H':
			res = append(res, []byte(fmt.Sprintf("%02d", t.Hour()))...)
		case 'i':
			res = append(res, []byte(fmt.Sprintf("%02d", t.Minute()))...)
		case 's':
			res = append(res, []byte(fmt.Sprintf("%02d", t.Second()))...)
		default:
			res = append(res, ch)
		}
	}
	return types.NewStringBytes(res)
}

func Strtotime(timeStr types.Value) types.Value {
	s := timeStr.String()
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02",
		time.RFC1123,
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return types.NewLong(t.Unix())
		}
	}
	return types.NewBool(false)
}
