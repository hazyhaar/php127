// SPDX-License-Identifier: Apache-2.0 OR MIT
// sql_scan — micro-noyaux C99 de balayage vectoriel AVX2 pour l'analyseur lexical SQL de mysqli
// et pour l'échappement de mysqli_real_escape_string.
//
// Chaque entrée publique interroge le processeur par __avx2() ; la marche
// scalaire reste la définition de référence. La marche vectorielle ne charge
// 32 octets que si pos + 32 <= end et laisse le reliquat à la queue scalaire.
// Côté Go, sgoiter abaisse __avx2() sur archsimd.X86.AVX2() et produit un
// compagnon scalaire sql_scan_nosimd_gen.go.

#include <stdint.h>
#include <stddef.h>
#include <immintrin.h>

static int __avx2(void) {
    return __builtin_cpu_supports("avx2");
}

// ------------------------------------------------------------- c2_sql_skip_spaces ----

// Espace ASCII du lexer SQL : ' ', '\t' (9), '\n' (10), '\v' (11), '\f' (12), '\r' (13).
static inline int is_sql_space(uint8_t c) {
    return (c == ' ') || (c >= 9 && c <= 13);
}

uint64_t c2_sql_skip_spaces_scalar(const uint8_t *src, uint64_t pos, uint64_t end) {
    while (pos < end) {
        uint8_t c = src[pos];
        if (!is_sql_space(c)) {
            return pos;
        }
        pos += 1;
    }
    return end;
}

__attribute__((target("avx2")))
uint64_t c2_sql_skip_spaces_avx2(const uint8_t *src, uint64_t pos, uint64_t end) {
    __m256i v_space;
    __m256i v_tab;
    __m256i v_ctl_lim;
    uint64_t hit;
    int found;

    v_space = _mm256_set1_epi8((char)' ');
    v_tab = _mm256_set1_epi8((char)9);
    v_ctl_lim = _mm256_set1_epi8((char)4);
    found = 0;
    hit = 0;

    while (pos + 32 <= end) {
        __m256i chunk = _mm256_loadu_si256((const __m256i *)(src + pos));

        __m256i eq_space = _mm256_cmpeq_epi8(chunk, v_space);

        __m256i d_ctl = _mm256_sub_epi8(chunk, v_tab);
        __m256i m_ctl = _mm256_min_epu8(d_ctl, v_ctl_lim);
        __m256i is_ctl = _mm256_cmpeq_epi8(m_ctl, d_ctl);

        __m256i valid = _mm256_or_si256(eq_space, is_ctl);

        uint32_t valid_mask = (uint32_t)_mm256_movemask_epi8(valid);
        uint32_t invalid_mask = ~valid_mask;
        if (invalid_mask != 0) {
            hit = pos + (uint64_t)__builtin_ctz(invalid_mask);
            found = 1;
            pos = end;
        } else {
            pos += 32;
        }
    }
    _mm256_zeroupper();
    if (found != 0) {
        return hit;
    }
    while (pos < end) {
        uint8_t c = src[pos];
        if (!is_sql_space(c)) {
            return pos;
        }
        pos += 1;
    }
    return end;
}

uint64_t c2_sql_skip_spaces(const uint8_t *src, uint64_t pos, uint64_t end) {
    if (__avx2()) {
        return c2_sql_skip_spaces_avx2(src, pos, end);
    }
    return c2_sql_skip_spaces_scalar(src, pos, end);
}

// ------------------------------------------------------------- c2_sql_scan_word ----

// Octet d'identifiant SQL non cité : [a-zA-Z0-9_$] ou octet >= 0x80.
static inline int is_sql_word_byte(uint8_t c) {
    return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || (c == '_') || (c == '$') || (c >= 0x80);
}

uint64_t c2_sql_scan_word_scalar(const uint8_t *src, uint64_t pos, uint64_t end) {
    while (pos < end) {
        uint8_t c = src[pos];
        if (!is_sql_word_byte(c)) {
            return pos;
        }
        pos += 1;
    }
    return end;
}

__attribute__((target("avx2")))
uint64_t c2_sql_scan_word_avx2(const uint8_t *src, uint64_t pos, uint64_t end) {
    __m256i v_under;
    __m256i v_dollar;
    __m256i v_zero;
    __m256i v_nine_lim;
    __m256i v_a_upper;
    __m256i v_a_lower;
    __m256i v_alpha_lim;
    uint64_t hit;
    int found;

    v_under = _mm256_set1_epi8((char)'_');
    v_dollar = _mm256_set1_epi8((char)'$');
    v_zero = _mm256_set1_epi8((char)'0');
    v_nine_lim = _mm256_set1_epi8((char)9);
    v_a_upper = _mm256_set1_epi8((char)'A');
    v_a_lower = _mm256_set1_epi8((char)'a');
    v_alpha_lim = _mm256_set1_epi8((char)25);
    found = 0;
    hit = 0;

    while (pos + 32 <= end) {
        __m256i chunk = _mm256_loadu_si256((const __m256i *)(src + pos));

        __m256i eq_under = _mm256_cmpeq_epi8(chunk, v_under);
        __m256i eq_dollar = _mm256_cmpeq_epi8(chunk, v_dollar);

        __m256i d_digit = _mm256_sub_epi8(chunk, v_zero);
        __m256i m_digit = _mm256_min_epu8(d_digit, v_nine_lim);
        __m256i is_digit = _mm256_cmpeq_epi8(m_digit, d_digit);

        __m256i d_upper = _mm256_sub_epi8(chunk, v_a_upper);
        __m256i m_upper = _mm256_min_epu8(d_upper, v_alpha_lim);
        __m256i is_upper = _mm256_cmpeq_epi8(m_upper, d_upper);

        __m256i d_lower = _mm256_sub_epi8(chunk, v_a_lower);
        __m256i m_lower = _mm256_min_epu8(d_lower, v_alpha_lim);
        __m256i is_lower = _mm256_cmpeq_epi8(m_lower, d_lower);

        __m256i valid0 = _mm256_or_si256(eq_under, eq_dollar);
        __m256i valid1 = _mm256_or_si256(valid0, is_digit);
        __m256i valid2 = _mm256_or_si256(is_upper, is_lower);
        __m256i valid_ascii = _mm256_or_si256(valid1, valid2);
        __m256i valid = _mm256_or_si256(valid_ascii, chunk);

        uint32_t valid_mask = (uint32_t)_mm256_movemask_epi8(valid);
        uint32_t invalid_mask = ~valid_mask;
        if (invalid_mask != 0) {
            hit = pos + (uint64_t)__builtin_ctz(invalid_mask);
            found = 1;
            pos = end;
        } else {
            pos += 32;
        }
    }
    _mm256_zeroupper();
    if (found != 0) {
        return hit;
    }
    while (pos < end) {
        uint8_t c = src[pos];
        if (!is_sql_word_byte(c)) {
            return pos;
        }
        pos += 1;
    }
    return end;
}

uint64_t c2_sql_scan_word(const uint8_t *src, uint64_t pos, uint64_t end) {
    if (__avx2()) {
        return c2_sql_scan_word_avx2(src, pos, end);
    }
    return c2_sql_scan_word_scalar(src, pos, end);
}

// ------------------------------------------------------------- c2_sql_find_escape ----

// Octet réécrit par mysqli_real_escape_string : 0x00, '\n', '\r', '\\', '\'', '"', 0x1a.
static inline int is_sql_escape_byte(uint8_t c) {
    return (c == 0) || (c == '\n') || (c == '\r') || (c == '\\') || (c == '\'') || (c == '"') || (c == 0x1a);
}

uint64_t c2_sql_find_escape_scalar(const uint8_t *src, uint64_t pos, uint64_t end) {
    while (pos < end) {
        uint8_t c = src[pos];
        if (is_sql_escape_byte(c)) {
            return pos;
        }
        pos += 1;
    }
    return end;
}

__attribute__((target("avx2")))
uint64_t c2_sql_find_escape_avx2(const uint8_t *src, uint64_t pos, uint64_t end) {
    __m256i v_nul;
    __m256i v_nl;
    __m256i v_cr;
    __m256i v_bs;
    __m256i v_sq;
    __m256i v_dq;
    __m256i v_sub;
    uint64_t hit;
    int found;

    v_nul = _mm256_set1_epi8((char)0);
    v_nl = _mm256_set1_epi8((char)'\n');
    v_cr = _mm256_set1_epi8((char)'\r');
    v_bs = _mm256_set1_epi8((char)'\\');
    v_sq = _mm256_set1_epi8((char)'\'');
    v_dq = _mm256_set1_epi8((char)'"');
    v_sub = _mm256_set1_epi8((char)0x1a);
    found = 0;
    hit = 0;

    while (pos + 32 <= end) {
        __m256i chunk = _mm256_loadu_si256((const __m256i *)(src + pos));

        __m256i e0 = _mm256_or_si256(_mm256_cmpeq_epi8(chunk, v_nul), _mm256_cmpeq_epi8(chunk, v_nl));
        __m256i e1 = _mm256_or_si256(_mm256_cmpeq_epi8(chunk, v_cr), _mm256_cmpeq_epi8(chunk, v_bs));
        __m256i e2 = _mm256_or_si256(_mm256_cmpeq_epi8(chunk, v_sq), _mm256_cmpeq_epi8(chunk, v_dq));
        __m256i e3 = _mm256_or_si256(e0, e1);
        __m256i e4 = _mm256_or_si256(e2, _mm256_cmpeq_epi8(chunk, v_sub));
        __m256i esc = _mm256_or_si256(e3, e4);

        uint32_t mask = (uint32_t)_mm256_movemask_epi8(esc);
        if (mask != 0) {
            hit = pos + (uint64_t)__builtin_ctz(mask);
            found = 1;
            pos = end;
        } else {
            pos += 32;
        }
    }
    _mm256_zeroupper();
    if (found != 0) {
        return hit;
    }
    while (pos < end) {
        uint8_t c = src[pos];
        if (is_sql_escape_byte(c)) {
            return pos;
        }
        pos += 1;
    }
    return end;
}

uint64_t c2_sql_find_escape(const uint8_t *src, uint64_t pos, uint64_t end) {
    if (__avx2()) {
        return c2_sql_find_escape_avx2(src, pos, end);
    }
    return c2_sql_find_escape_scalar(src, pos, end);
}
