// SPDX-License-Identifier: Apache-2.0 OR MIT
// utf8_scan — conversions entre rang de point de code et décalage en octets
// dans une chaîne UTF-8, sans allocation, pour mb_substr, mb_strpos et
// mb_strcut.
//
// Le découpage en points de code est celui du décodeur UTF-8 de mbstring
// (PHP 8.3) : une séquence invalide forme un seul segment, sa plus longue
// sous-partie valide ; mb_strlen("\xe2\x82") vaut 1, mb_strlen("\x80\x81")
// vaut 2. Le découpage de mb_strcut est celui de la table mblen_table_utf8
// de libmbfl, qui ne lit que l'octet de tête.
//
// Chaque entrée publique interroge le processeur par __avx2() ; la marche
// vectorielle ne saute que des blocs de 32 octets dont chaque octet forme à
// lui seul un segment, puis rend la main à la marche scalaire, qui reste la
// définition de référence du résultat.

#include <stdint.h>
#include <stddef.h>
#include <immintrin.h>

// Sonde de jeu d'instructions. Sous gcc, __builtin_cpu_supports interroge
// CPUID au premier appel ; sous sgoiter, l'appel s'abaisse sur la sonde
// équivalente de simd/archsimd.
static int __avx2(void) {
    return __builtin_cpu_supports("avx2");
}

// Longueur en octets (au moins 1) du segment qui commence à s[i], i < len.
static uint64_t c2_utf8_seg(const uint8_t *s, uint64_t len, uint64_t i) {
    uint8_t b0 = s[i];
    uint8_t b1;
    if (b0 < 0x80) {
        return 1;
    }
    if (b0 < 0xC2 || b0 > 0xF4) {
        return 1;
    }
    if (i + 1 >= len) {
        return 1;
    }
    b1 = s[i + 1];
    if ((b1 & 0xC0) != 0x80) {
        return 1;
    }
    if (b0 < 0xE0) {
        return 2;
    }
    if (b0 < 0xF0) {
        if (b0 == 0xE0 && b1 < 0xA0) {
            return 1;
        }
        if (b0 == 0xED && b1 >= 0xA0) {
            return 1;
        }
        if (i + 2 < len && (s[i + 2] & 0xC0) == 0x80) {
            return 3;
        }
        return 2;
    }
    if (b0 == 0xF0 && b1 < 0x90) {
        return 1;
    }
    if (b0 == 0xF4 && b1 >= 0x90) {
        return 1;
    }
    if (i + 2 >= len) {
        return 2;
    }
    if ((s[i + 2] & 0xC0) != 0x80) {
        return 2;
    }
    if (i + 3 < len && (s[i + 3] & 0xC0) == 0x80) {
        return 4;
    }
    return 3;
}

// Longueur d'un caractère selon l'octet de tête, table mblen_table_utf8 de
// libmbfl (PHP 8.3) : 0xC0, 0xC1 et 0xF5 à 0xFF comptent pour un octet.
static uint64_t c2_utf8_mblen(uint8_t b) {
    if (b < 0xC2) {
        return 1;
    }
    if (b < 0xE0) {
        return 2;
    }
    if (b < 0xF0) {
        return 3;
    }
    if (b < 0xF5) {
        return 4;
    }
    return 1;
}

static uint64_t c2_utf8_char_to_byte_scalar(const uint8_t *s, uint64_t len, uint64_t char_idx) {
    uint64_t i = 0;
    uint64_t c = 0;
    while (c < char_idx && i < len) {
        i += c2_utf8_seg(s, len, i);
        c += 1;
    }
    return i;
}

// Saut vectoriel : un bloc de 32 octets sans bit de poids fort est formé de
// 32 segments ASCII ; il n'est franchi d'un coup que s'il reste au moins 32
// points de code à parcourir.
__attribute__((target("avx2")))
static uint64_t c2_utf8_char_to_byte_avx2(const uint8_t *s, uint64_t len, uint64_t char_idx) {
    __m256i chunk;
    uint32_t bits;
    uint64_t i = 0;
    uint64_t c = 0;
    while (c < char_idx && i < len) {
        bits = 1;
        if (i + 32 <= len && char_idx - c >= 32) {
            chunk = _mm256_loadu_si256((const __m256i *)(s + i));
            bits = (uint32_t)_mm256_movemask_epi8(chunk);
        }
        if (bits == 0) {
            i += 32;
            c += 32;
        } else {
            i += c2_utf8_seg(s, len, i);
            c += 1;
        }
    }
    _mm256_zeroupper();
    return i;
}

// Décalage en octets du point de code de rang char_idx, ou len si la chaîne
// en compte moins.
uint64_t c2_utf8_char_to_byte(const uint8_t *s, uint64_t len, uint64_t char_idx) {
    if (__avx2()) {
        return c2_utf8_char_to_byte_avx2(s, len, char_idx);
    }
    return c2_utf8_char_to_byte_scalar(s, len, char_idx);
}

static uint64_t c2_utf8_byte_to_char_scalar(const uint8_t *s, uint64_t byte_offset) {
    uint64_t i = 0;
    uint64_t n = 0;
    while (i < byte_offset) {
        i += c2_utf8_seg(s, byte_offset, i);
        n += 1;
    }
    return n;
}

__attribute__((target("avx2")))
static uint64_t c2_utf8_byte_to_char_avx2(const uint8_t *s, uint64_t byte_offset) {
    __m256i chunk;
    uint32_t bits;
    uint64_t i = 0;
    uint64_t n = 0;
    while (i < byte_offset) {
        bits = 1;
        if (i + 32 <= byte_offset) {
            chunk = _mm256_loadu_si256((const __m256i *)(s + i));
            bits = (uint32_t)_mm256_movemask_epi8(chunk);
        }
        if (bits == 0) {
            i += 32;
            n += 32;
        } else {
            i += c2_utf8_seg(s, byte_offset, i);
            n += 1;
        }
    }
    _mm256_zeroupper();
    return n;
}

// Nombre de points de code de s[0:byte_offset], découpés comme mb_strlen.
uint64_t c2_utf8_byte_to_char(const uint8_t *s, uint64_t byte_offset) {
    if (__avx2()) {
        return c2_utf8_byte_to_char_avx2(s, byte_offset);
    }
    return c2_utf8_byte_to_char_scalar(s, byte_offset);
}

static uint64_t c2_utf8_cut_floor_scalar(const uint8_t *s, uint64_t from, uint64_t pos) {
    uint64_t p = from;
    uint64_t m = 0;
    while (p < pos) {
        m = c2_utf8_mblen(s[p]);
        p += m;
    }
    if (p > pos) {
        p -= m;
    }
    return p;
}

// Saut vectoriel : un bloc de 32 octets sans octet de la forme 11xxxxxx ne
// contient que des caractères d'un octet selon la table mblen.
__attribute__((target("avx2")))
static uint64_t c2_utf8_cut_floor_avx2(const uint8_t *s, uint64_t from, uint64_t pos) {
    __m256i lead_mask;
    __m256i chunk;
    __m256i eq;
    uint32_t bits;
    uint64_t p = from;
    uint64_t m = 0;
    lead_mask = _mm256_set1_epi8((char)0xC0);
    while (p < pos) {
        bits = 1;
        if (p + 32 <= pos) {
            chunk = _mm256_loadu_si256((const __m256i *)(s + p));
            eq = _mm256_cmpeq_epi8(_mm256_and_si256(chunk, lead_mask), lead_mask);
            bits = (uint32_t)_mm256_movemask_epi8(eq);
        }
        if (bits == 0) {
            m = 1;
            p += 32;
        } else {
            m = c2_utf8_mblen(s[p]);
            p += m;
        }
    }
    _mm256_zeroupper();
    if (p > pos) {
        p -= m;
    }
    return p;
}

// Frontière de mb_strcut : depuis le début de caractère from, avance de
// caractère en caractère tant que la position reste sous pos, puis recule
// d'un caractère si le dernier pas a franchi pos. Exige pos <= len, ou un
// pas qui ne lit jamais au-delà de len (chaque lecture se fait sous pos).
uint64_t c2_utf8_cut_floor(const uint8_t *s, uint64_t from, uint64_t pos) {
    if (__avx2()) {
        return c2_utf8_cut_floor_avx2(s, from, pos);
    }
    return c2_utf8_cut_floor_scalar(s, from, pos);
}
