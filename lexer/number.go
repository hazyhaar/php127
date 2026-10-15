package lexer

import "math"

func digit(c byte) uint64 {
	if c >= '0' && c <= '9' {
		return uint64(c - '0')
	}
	c = lower(c)
	if c >= 'a' && c <= 'f' {
		return uint64(c - 'a' + 10)
	}
	return 16
}

func digits(s string, p int, base uint64) int {
	for p < len(s) {
		if digit(s[p]) < base {
			p++
			continue
		}
		if s[p] == '_' && p+1 < len(s) && digit(s[p+1]) < base {
			p += 2
			continue
		}
		break
	}
	return p
}

func number(s string, p int, floating bool) (int, ID) {
	start, base := p, uint64(10)
	if p+2 < len(s) && s[p] == '0' {
		switch lower(s[p+1]) {
		case 'x':
			base = 16
		case 'b':
			base = 2
		case 'o':
			base = 8
		}
		if base != 10 {
			if digit(s[p+2]) < base {
				start = p + 2
			} else {
				base = 10
			}
		}
	}
	end := digits(s, start, base)
	id := T_LNUMBER
	if base == 10 && floating {
		if end < len(s) && s[end] == '.' {
			end++
			if end < len(s) && digit(s[end]) < 10 {
				end = digits(s, end, 10)
			}
			id = T_DNUMBER
		}
		if end < len(s) && lower(s[end]) == 'e' {
			exp := end + 1
			if exp < len(s) && (s[exp] == '+' || s[exp] == '-') {
				exp++
			}
			if exp < len(s) && digit(s[exp]) < 10 {
				end = digits(s, exp, 10)
				id = T_DNUMBER
			}
		}
	}
	if id == T_LNUMBER {
		if base == 10 && s[p] == '0' {
			base = 8
		}
		value := uint64(0)
		for i := start; i < end; i++ {
			if s[i] == '_' {
				continue
			}
			d := digit(s[i])
			if d >= base {
				break
			}
			if value > (math.MaxInt64-d)/base {
				id = T_DNUMBER
				break
			}
			value = value*base + d
		}
	}
	return end, id
}
