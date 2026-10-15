// SPDX-License-Identifier: Apache-2.0 OR MIT
// php_scan — micro-noyaux C99 de balayage vectoriel AVX2 pour l'analyseur lexical PHP 8.3.6.
//
// Chaque entrée publique interroge le processeur par __avx2() ; la marche
// scalaire reste la définition de référence. La marche vectorielle ne charge
// 32 octets que si pos + 32 <= end et laisse le reliquat à la queue scalaire.
// Côté Go, sgoiter abaisse __avx2() sur archsimd.X86.AVX2() et produit un
// compagnon scalaire php_scan_nosimd_gen.go.

#include <stdint.h>
#include <stddef.h>
#include <immintrin.h>

static int __avx2(void) {
    return __builtin_cpu_supports("avx2");
}

// ------------------------------------------------------------- php_scan_label ----

static inline int is_php_label_part(uint8_t c) {
    return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || (c == '_') || (c >= 0x80);
}

int php_scan_label_scalar(const uint8_t *s, int pos, int end) {
    while (pos < end) {
        uint8_t c = s[pos];
        if (!is_php_label_part(c)) {
            return pos;
        }
        pos += 1;
    }
    return end;
}

__attribute__((target("avx2")))
int php_scan_label_avx2(const uint8_t *s, int pos, int end) {
    __m256i v_under;
    __m256i v_zero;
    __m256i v_nine_lim;
    __m256i v_a_upper;
    __m256i v_a_lower;
    __m256i v_alpha_lim;
    int found;
    int hit;

    v_under = _mm256_set1_epi8((char)'_');
    v_zero = _mm256_set1_epi8((char)'0');
    v_nine_lim = _mm256_set1_epi8((char)9);
    v_a_upper = _mm256_set1_epi8((char)'A');
    v_a_lower = _mm256_set1_epi8((char)'a');
    v_alpha_lim = _mm256_set1_epi8((char)25);
    found = 0;
    hit = 0;

    while (pos + 32 <= end) {
        __m256i chunk = _mm256_loadu_si256((const __m256i *)(s + pos));

        __m256i eq_under = _mm256_cmpeq_epi8(chunk, v_under);

        __m256i d_digit = _mm256_sub_epi8(chunk, v_zero);
        __m256i m_digit = _mm256_min_epu8(d_digit, v_nine_lim);
        __m256i is_digit = _mm256_cmpeq_epi8(m_digit, d_digit);

        __m256i d_upper = _mm256_sub_epi8(chunk, v_a_upper);
        __m256i m_upper = _mm256_min_epu8(d_upper, v_alpha_lim);
        __m256i is_upper = _mm256_cmpeq_epi8(m_upper, d_upper);

        __m256i d_lower = _mm256_sub_epi8(chunk, v_a_lower);
        __m256i m_lower = _mm256_min_epu8(d_lower, v_alpha_lim);
        __m256i is_lower = _mm256_cmpeq_epi8(m_lower, d_lower);

        __m256i valid1 = _mm256_or_si256(eq_under, is_digit);
        __m256i valid2 = _mm256_or_si256(is_upper, is_lower);
        __m256i valid_ascii = _mm256_or_si256(valid1, valid2);
        __m256i valid = _mm256_or_si256(valid_ascii, chunk);

        uint32_t valid_mask = (uint32_t)_mm256_movemask_epi8(valid);
        uint32_t invalid_mask = ~valid_mask;
        if (invalid_mask != 0) {
            hit = pos + (int)__builtin_ctz(invalid_mask);
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
        uint8_t c = s[pos];
        if (!is_php_label_part(c)) {
            return pos;
        }
        pos += 1;
    }
    return end;
}

int php_scan_label(const uint8_t *s, int pos, int end) {
    if (__avx2()) {
        return php_scan_label_avx2(s, pos, end);
    }
    return php_scan_label_scalar(s, pos, end);
}

// ------------------------------------------------------------- php_scan_line_comment ----

int php_scan_line_comment_scalar(const uint8_t *s, int pos, int end) {
    while (pos < end) {
        uint8_t c = s[pos];
        if (c == '\r' || c == '\n' || c == '?') {
            return pos;
        }
        pos += 1;
    }
    return end;
}

__attribute__((target("avx2")))
int php_scan_line_comment_avx2(const uint8_t *s, int pos, int end) {
    __m256i cr;
    __m256i nl;
    __m256i qm;
    __m256i chunk;
    uint32_t mask;
    int hit;
    int found;

    cr = _mm256_set1_epi8((char)'\r');
    nl = _mm256_set1_epi8((char)'\n');
    qm = _mm256_set1_epi8((char)'?');
    found = 0;
    hit = 0;
    while (pos + 32 <= end) {
        chunk = _mm256_loadu_si256((const __m256i *)(s + pos));
        mask = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(chunk, cr))
             | (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(chunk, nl))
             | (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(chunk, qm));
        if (mask != 0) {
            hit = pos + (int)__builtin_ctz(mask);
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
        uint8_t c = s[pos];
        if (c == '\r' || c == '\n' || c == '?') {
            return pos;
        }
        pos += 1;
    }
    return end;
}

int php_scan_line_comment(const uint8_t *s, int pos, int end) {
    if (__avx2()) {
        return php_scan_line_comment_avx2(s, pos, end);
    }
    return php_scan_line_comment_scalar(s, pos, end);
}

// ------------------------------------------------------------- php_scan_single_quote ----

int php_scan_single_quote_scalar(const uint8_t *s, int pos, int end) {
    while (pos < end) {
        uint8_t c = s[pos];
        if (c == '\'' || c == '\\') {
            return pos;
        }
        pos += 1;
    }
    return end;
}

__attribute__((target("avx2")))
int php_scan_single_quote_avx2(const uint8_t *s, int pos, int end) {
    __m256i sq;
    __m256i bs;
    __m256i chunk;
    uint32_t mask;
    int hit;
    int found;

    sq = _mm256_set1_epi8((char)'\'');
    bs = _mm256_set1_epi8((char)'\\');
    found = 0;
    hit = 0;
    while (pos + 32 <= end) {
        chunk = _mm256_loadu_si256((const __m256i *)(s + pos));
        mask = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(chunk, sq))
             | (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(chunk, bs));
        if (mask != 0) {
            hit = pos + (int)__builtin_ctz(mask);
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
        uint8_t c = s[pos];
        if (c == '\'' || c == '\\') {
            return pos;
        }
        pos += 1;
    }
    return end;
}

int php_scan_single_quote(const uint8_t *s, int pos, int end) {
    if (__avx2()) {
        return php_scan_single_quote_avx2(s, pos, end);
    }
    return php_scan_single_quote_scalar(s, pos, end);
}

// ------------------------------------------------------------- php_scan_double_quote ----

int php_scan_double_quote_scalar(const uint8_t *s, int pos, int end) {
    while (pos < end) {
        uint8_t c = s[pos];
        if (c == '"' || c == '\\' || c == '$' || c == '{') {
            return pos;
        }
        pos += 1;
    }
    return end;
}

__attribute__((target("avx2")))
int php_scan_double_quote_avx2(const uint8_t *s, int pos, int end) {
    __m256i dq;
    __m256i bs;
    __m256i dl;
    __m256i lb;
    __m256i chunk;
    uint32_t mask;
    int hit;
    int found;

    dq = _mm256_set1_epi8((char)'"');
    bs = _mm256_set1_epi8((char)'\\');
    dl = _mm256_set1_epi8((char)'$');
    lb = _mm256_set1_epi8((char)'{');
    found = 0;
    hit = 0;
    while (pos + 32 <= end) {
        chunk = _mm256_loadu_si256((const __m256i *)(s + pos));
        mask = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(chunk, dq))
             | (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(chunk, bs))
             | (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(chunk, dl))
             | (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(chunk, lb));
        if (mask != 0) {
            hit = pos + (int)__builtin_ctz(mask);
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
        uint8_t c = s[pos];
        if (c == '"' || c == '\\' || c == '$' || c == '{') {
            return pos;
        }
        pos += 1;
    }
    return end;
}

int php_scan_double_quote(const uint8_t *s, int pos, int end) {
    if (__avx2()) {
        return php_scan_double_quote_avx2(s, pos, end);
    }
    return php_scan_double_quote_scalar(s, pos, end);
}

// ------------------------------------------------------------- php_scan_inline_html ----

int php_scan_inline_html_scalar(const uint8_t *s, int pos, int end) {
    while (pos < end) {
        uint8_t c = s[pos];
        if (c == '<') {
            return pos;
        }
        pos += 1;
    }
    return end;
}

__attribute__((target("avx2")))
int php_scan_inline_html_avx2(const uint8_t *s, int pos, int end) {
    __m256i lt;
    __m256i chunk;
    uint32_t mask;
    int hit;
    int found;

    lt = _mm256_set1_epi8((char)'<');
    found = 0;
    hit = 0;
    while (pos + 32 <= end) {
        chunk = _mm256_loadu_si256((const __m256i *)(s + pos));
        mask = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(chunk, lt));
        if (mask != 0) {
            hit = pos + (int)__builtin_ctz(mask);
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
        uint8_t c = s[pos];
        if (c == '<') {
            return pos;
        }
        pos += 1;
    }
    return end;
}

int php_scan_inline_html(const uint8_t *s, int pos, int end) {
    if (__avx2()) {
        return php_scan_inline_html_avx2(s, pos, end);
    }
    return php_scan_inline_html_scalar(s, pos, end);
}

// ------------------------------------------------------------- php_scan_block_comment ----

int php_scan_block_comment_scalar(const uint8_t *s, int pos, int end) {
    while (pos + 1 < end) {
        if (s[pos] == '*' && s[pos + 1] == '/') {
            return pos;
        }
        pos += 1;
    }
    return -1;
}

__attribute__((target("avx2")))
int php_scan_block_comment_avx2(const uint8_t *s, int pos, int end) {
    __m256i star;
    __m256i slash;
    __m256i c0;
    __m256i c1;
    __m256i eq0;
    __m256i eq1;
    __m256i both;
    uint32_t mask;
    int hit;
    int found;

    star = _mm256_set1_epi8((char)'*');
    slash = _mm256_set1_epi8((char)'/');
    found = 0;
    hit = 0;
    while (pos + 33 <= end) {
        c0 = _mm256_loadu_si256((const __m256i *)(s + pos));
        c1 = _mm256_loadu_si256((const __m256i *)(s + pos + 1));
        eq0 = _mm256_cmpeq_epi8(c0, star);
        eq1 = _mm256_cmpeq_epi8(c1, slash);
        both = _mm256_and_si256(eq0, eq1);
        mask = (uint32_t)_mm256_movemask_epi8(both);
        if (mask != 0) {
            hit = pos + (int)__builtin_ctz(mask);
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
    while (pos + 1 < end) {
        if (s[pos] == '*' && s[pos + 1] == '/') {
            return pos;
        }
        pos += 1;
    }
    return -1;
}

int php_scan_block_comment(const uint8_t *s, int pos, int end) {
    if (__avx2()) {
        return php_scan_block_comment_avx2(s, pos, end);
    }
    return php_scan_block_comment_scalar(s, pos, end);
}

