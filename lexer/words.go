package lexer

import "unsafe"

// Résolution O(1) des mots-clés PHP via la table ARCHTIME PhpKwTable et
// l'opérateur pyramidal c2_kw_resolve généré par sgoiter.
func keywordAt(src string, p, end int) ID {
	n := end - p
	if n == 0 || n > 15 {
		return T_STRING
	}
	if p+16 <= len(src) {
		buf := unsafe.Slice(unsafe.StringData(src[p:]), 16)
		return ID(C2_kw_resolve_pyramid(&PhpKwTable, buf, uint32(n), uint16(T_STRING)))
	}
	var pad [16]byte
	copy(pad[:], src[p:end])
	return ID(C2_kw_resolve_pyramid(&PhpKwTable, pad[:], uint32(n), uint16(T_STRING)))
}

// Résolution O(1) des types de cast PHP via la table ARCHTIME PhpCastTable et
// l'opérateur pyramidal c2_kw_resolve généré par sgoiter.
func castAt(src string, p, end int) ID {
	n := end - p
	if n == 0 || n > 15 {
		return EOF
	}
	if p+16 <= len(src) {
		buf := unsafe.Slice(unsafe.StringData(src[p:]), 16)
		return ID(C2_kw_resolve_pyramid(&PhpCastTable, buf, uint32(n), uint16(EOF)))
	}
	var pad [16]byte
	copy(pad[:], src[p:end])
	return ID(C2_kw_resolve_pyramid(&PhpCastTable, pad[:], uint32(n), uint16(EOF)))
}

// Résolution ARCHTIME sans map Go des opérateurs multi-octets (2 ou 3 octets).
func operatorAt(s string, p int) (ID, int) {
	rem := len(s) - p
	if rem < 2 {
		return EOF, 0
	}
	c0, c1 := s[p], s[p+1]
	if rem >= 3 {
		c2 := s[p+2]
		switch uint32(c0)<<16 | uint32(c1)<<8 | uint32(c2) {
		case uint32('?')<<16 | uint32('-')<<8 | uint32('>'):
			return T_NULLSAFE_OBJECT_OPERATOR, 3
		case uint32('.')<<16 | uint32('.')<<8 | uint32('.'):
			return T_ELLIPSIS, 3
		case uint32('?')<<16 | uint32('?')<<8 | uint32('='):
			return T_COALESCE_EQUAL, 3
		case uint32('=')<<16 | uint32('=')<<8 | uint32('='):
			return T_IS_IDENTICAL, 3
		case uint32('!')<<16 | uint32('=')<<8 | uint32('='):
			return T_IS_NOT_IDENTICAL, 3
		case uint32('<')<<16 | uint32('=')<<8 | uint32('>'):
			return T_SPACESHIP, 3
		case uint32('*')<<16 | uint32('*')<<8 | uint32('='):
			return T_POW_EQUAL, 3
		case uint32('<')<<16 | uint32('<')<<8 | uint32('='):
			return T_SL_EQUAL, 3
		case uint32('>')<<16 | uint32('>')<<8 | uint32('='):
			return T_SR_EQUAL, 3
		}
	}
	switch uint16(c0)<<8 | uint16(c1) {
	case uint16('-')<<8 | uint16('>'):
		return T_OBJECT_OPERATOR, 2
	case uint16(':')<<8 | uint16(':'):
		return T_PAAMAYIM_NEKUDOTAYIM, 2
	case uint16('?')<<8 | uint16('?'):
		return T_COALESCE, 2
	case uint16('=')<<8 | uint16('>'):
		return T_DOUBLE_ARROW, 2
	case uint16('+')<<8 | uint16('+'):
		return T_INC, 2
	case uint16('-')<<8 | uint16('-'):
		return T_DEC, 2
	case uint16('=')<<8 | uint16('='):
		return T_IS_EQUAL, 2
	case uint16('!')<<8 | uint16('='), uint16('<')<<8 | uint16('>'):
		return T_IS_NOT_EQUAL, 2
	case uint16('<')<<8 | uint16('='):
		return T_IS_SMALLER_OR_EQUAL, 2
	case uint16('>')<<8 | uint16('='):
		return T_IS_GREATER_OR_EQUAL, 2
	case uint16('+')<<8 | uint16('='):
		return T_PLUS_EQUAL, 2
	case uint16('-')<<8 | uint16('='):
		return T_MINUS_EQUAL, 2
	case uint16('*')<<8 | uint16('='):
		return T_MUL_EQUAL, 2
	case uint16('*')<<8 | uint16('*'):
		return T_POW, 2
	case uint16('/')<<8 | uint16('='):
		return T_DIV_EQUAL, 2
	case uint16('.')<<8 | uint16('='):
		return T_CONCAT_EQUAL, 2
	case uint16('%')<<8 | uint16('='):
		return T_MOD_EQUAL, 2
	case uint16('&')<<8 | uint16('='):
		return T_AND_EQUAL, 2
	case uint16('|')<<8 | uint16('='):
		return T_OR_EQUAL, 2
	case uint16('^')<<8 | uint16('='):
		return T_XOR_EQUAL, 2
	case uint16('|')<<8 | uint16('|'):
		return T_BOOLEAN_OR, 2
	case uint16('&')<<8 | uint16('&'):
		return T_BOOLEAN_AND, 2
	case uint16('<')<<8 | uint16('<'):
		return T_SL, 2
	case uint16('>')<<8 | uint16('>'):
		return T_SR, 2
	}
	return EOF, 0
}
