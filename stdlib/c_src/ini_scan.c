// SPDX-License-Identifier: Apache-2.0 OR MIT
// ini_scan — micro-noyaux de l'analyseur INI de parse_ini_string (zend_ini_scanner.l), forme
// vectorielle AVX2.
//
// Chaque entrée publique interroge le processeur par __avx2() et n'emprunte la marche vectorielle
// que si AVX2 répond présent et qu'il reste au moins 32 octets ; sinon elle déroule la marche
// scalaire, qui reste la définition de référence du résultat. Les corps vectoriels portent
// __attribute__((target("avx2"))) : gcc les compile en AVX2 sans imposer -mavx2 à l'unité de
// traduction entière.
//
// Côté Go, sgoiter abaisse __avx2() sur archsimd.X86.AVX2() et produit un compagnon scalaire
// d'où toute branche vectorielle a disparu.
//
// Convention de position : pos et end sont des index absolus dans src, end <= longueur de src ;
// chaque balayage rend l'index du premier octet qui l'arrête, ou end.

#include <stdint.h>
#include <stddef.h>
#include <immintrin.h>

static int __avx2(void) {
    return __builtin_cpu_supports("avx2");
}

// ------------------------------------------------------------- hspace ----

// {TABS} de zend_ini_scanner.l : espaces et tabulations horizontales.
static uint64_t c2_ini_skip_hspace_scalar(const uint8_t *src, uint64_t pos, uint64_t end) {
    while (pos < end && (src[pos] == ' ' || src[pos] == '\t')) {
        pos += 1;
    }
    return pos;
}

// 32 octets par tour : égalité à ' ' ou '\t' (VPCMPEQB × 2, VPOR), bitmap (VPMOVMSKB), premier
// bit à zéro du bitmap.
__attribute__((target("avx2")))
static uint64_t c2_ini_skip_hspace_avx2(const uint8_t *src, uint64_t pos, uint64_t end) {
    __m256i v_space;
    __m256i v_tab;
    __m256i chunk;
    __m256i hit;
    uint32_t mask;
    uint64_t stop;
    int found;

    v_space = _mm256_set1_epi8(' ');
    v_tab = _mm256_set1_epi8('\t');
    found = 0;
    stop = end;
    while (pos + 32 <= end) {
        chunk = _mm256_loadu_si256((const __m256i *)(src + pos));
        hit = _mm256_or_si256(_mm256_cmpeq_epi8(chunk, v_space), _mm256_cmpeq_epi8(chunk, v_tab));
        mask = (uint32_t)_mm256_movemask_epi8(hit) ^ 0xFFFFFFFFu;
        if (mask != 0) {
            stop = pos + (uint64_t)__builtin_ctz(mask);
            found = 1;
            pos = end;
        } else {
            pos += 32;
        }
    }
    _mm256_zeroupper();
    if (found != 0) {
        return stop;
    }
    return c2_ini_skip_hspace_scalar(src, pos, end);
}

uint64_t c2_ini_skip_hspace(const uint8_t *src, uint64_t pos, uint64_t end) {
    if (pos + 32 <= end && __avx2()) {
        return c2_ini_skip_hspace_avx2(src, pos, end);
    }
    return c2_ini_skip_hspace_scalar(src, pos, end);
}

// ---------------------------------------------------------------- eol ----

// Premier '\n' ou '\r' à partir de pos.
static uint64_t c2_ini_find_eol_scalar(const uint8_t *src, uint64_t pos, uint64_t end) {
    while (pos < end && src[pos] != '\n' && src[pos] != '\r') {
        pos += 1;
    }
    return pos;
}

// 32 octets par tour : égalité à '\n' ou '\r', bitmap, premier bit à un.
__attribute__((target("avx2")))
static uint64_t c2_ini_find_eol_avx2(const uint8_t *src, uint64_t pos, uint64_t end) {
    __m256i v_lf;
    __m256i v_cr;
    __m256i chunk;
    __m256i hit;
    uint32_t mask;
    uint64_t stop;
    int found;

    v_lf = _mm256_set1_epi8('\n');
    v_cr = _mm256_set1_epi8('\r');
    found = 0;
    stop = end;
    while (pos + 32 <= end) {
        chunk = _mm256_loadu_si256((const __m256i *)(src + pos));
        hit = _mm256_or_si256(_mm256_cmpeq_epi8(chunk, v_lf), _mm256_cmpeq_epi8(chunk, v_cr));
        mask = (uint32_t)_mm256_movemask_epi8(hit);
        if (mask != 0) {
            stop = pos + (uint64_t)__builtin_ctz(mask);
            found = 1;
            pos = end;
        } else {
            pos += 32;
        }
    }
    _mm256_zeroupper();
    if (found != 0) {
        return stop;
    }
    return c2_ini_find_eol_scalar(src, pos, end);
}

uint64_t c2_ini_find_eol(const uint8_t *src, uint64_t pos, uint64_t end) {
    if (pos + 32 <= end && __avx2()) {
        return c2_ini_find_eol_avx2(src, pos, end);
    }
    return c2_ini_find_eol_scalar(src, pos, end);
}

// ---------------------------------------------------------------- key ----

// Classe LABEL de zend_ini_scanner.l, [^=\n\r\t;&|^$~(){}!"\[\]\x00]+ : l'espace appartient à la
// clé (parse_ini_string("a b=1") rend la clé "a b" sous PHP 8.3).
static int c2_ini_key_stop(uint8_t c) {
    switch (c) {
    case 0x00: case '\t': case '\n': case '\r':
    case '!': case '"': case '$': case '&': case '(': case ')':
    case ';': case '=':
    case '[': case ']': case '^':
    case '{': case '|': case '}': case '~':
        return 1;
    default:
        return 0;
    }
}

static uint64_t c2_ini_scan_key_scalar(const uint8_t *src, uint64_t pos, uint64_t end) {
    while (pos < end && c2_ini_key_stop(src[pos]) == 0) {
        pos += 1;
    }
    return pos;
}

// Classement par quartets (VPSHUFB × 2) : chaque octet d'arrêt appartient à l'un de cinq groupes
// de même quartet haut (0x0_, 0x2_, 0x3_, 0x5_, 0x7_) ; hi_tab porte le bit du groupe, lo_tab
// l'union des bits des groupes qui contiennent ce quartet bas. L'octet est un arrêt si et
// seulement si hi_tab[hi] & lo_tab[lo] est non nul. Un octet ≥ 0x80 a un quartet haut sans groupe.
__attribute__((target("avx2")))
static uint64_t c2_ini_scan_key_avx2(const uint8_t *src, uint64_t pos, uint64_t end) {
    __m256i lo_tab;
    __m256i hi_tab;
    __m256i nib;
    __m256i chunk;
    __m256i cls;
    __m256i zero;
    uint32_t mask;
    uint64_t stop;
    int found;

    // Bits : 1 = 0x0_ {0,9,A,D} ; 2 = 0x2_ {1,2,4,6,8,9} ; 4 = 0x3_ {B,D} ; 8 = 0x5_ {B,D,E} ;
    // 16 = 0x7_ {B,C,D,E}.
    lo_tab = _mm256_setr_epi8(
        1, 2, 2, 0, 2, 0, 2, 0, 2, 3, 1, 28, 16, 29, 24, 0,
        1, 2, 2, 0, 2, 0, 2, 0, 2, 3, 1, 28, 16, 29, 24, 0);
    hi_tab = _mm256_setr_epi8(
        1, 0, 2, 4, 0, 8, 0, 16, 0, 0, 0, 0, 0, 0, 0, 0,
        1, 0, 2, 4, 0, 8, 0, 16, 0, 0, 0, 0, 0, 0, 0, 0);
    nib = _mm256_set1_epi8(0x0F);
    zero = _mm256_setzero_si256();
    found = 0;
    stop = end;
    while (pos + 32 <= end) {
        chunk = _mm256_loadu_si256((const __m256i *)(src + pos));
        cls = _mm256_and_si256(
            _mm256_shuffle_epi8(lo_tab, _mm256_and_si256(chunk, nib)),
            _mm256_shuffle_epi8(hi_tab, _mm256_and_si256(_mm256_srli_epi16(chunk, 4), nib)));
        mask = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(cls, zero)) ^ 0xFFFFFFFFu;
        if (mask != 0) {
            stop = pos + (uint64_t)__builtin_ctz(mask);
            found = 1;
            pos = end;
        } else {
            pos += 32;
        }
    }
    _mm256_zeroupper();
    if (found != 0) {
        return stop;
    }
    return c2_ini_scan_key_scalar(src, pos, end);
}

uint64_t c2_ini_scan_key(const uint8_t *src, uint64_t pos, uint64_t end) {
    if (pos + 32 <= end && __avx2()) {
        return c2_ini_scan_key_avx2(src, pos, end);
    }
    return c2_ini_scan_key_scalar(src, pos, end);
}

// ----------------------------------------------------------- keywords ----

// Mots-clés de zend_ini_scanner.l, insensibles à la casse ASCII, sur exactement len octets :
// 1 pour true/on/yes, 2 pour false/off/no/none, 3 pour null, 0 sinon. (c | 0x20) == lettre
// minuscule ne vaut que pour la lettre et sa majuscule.
int64_t c2_ini_kw_code(const uint8_t *src, uint64_t len) {
    uint8_t c0;
    uint8_t c1;
    uint8_t c2;
    uint8_t c3;
    uint8_t c4;

    if (len < 2 || len > 5) {
        return 0;
    }
    c0 = src[0] | 0x20;
    c1 = src[1] | 0x20;
    if (len == 2) {
        if (c0 == 'o' && c1 == 'n') {
            return 1;
        }
        if (c0 == 'n' && c1 == 'o') {
            return 2;
        }
        return 0;
    }
    c2 = src[2] | 0x20;
    if (len == 3) {
        if (c0 == 'y' && c1 == 'e' && c2 == 's') {
            return 1;
        }
        if (c0 == 'o' && c1 == 'f' && c2 == 'f') {
            return 2;
        }
        return 0;
    }
    c3 = src[3] | 0x20;
    if (len == 4) {
        if (c0 == 't' && c1 == 'r' && c2 == 'u' && c3 == 'e') {
            return 1;
        }
        if (c0 == 'n' && c1 == 'o' && c2 == 'n' && c3 == 'e') {
            return 2;
        }
        if (c0 == 'n' && c1 == 'u' && c2 == 'l' && c3 == 'l') {
            return 3;
        }
        return 0;
    }
    c4 = src[4] | 0x20;
    if (c0 == 'f' && c1 == 'a' && c2 == 'l' && c3 == 's' && c4 == 'e') {
        return 2;
    }
    return 0;
}
