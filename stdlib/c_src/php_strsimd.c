// SPDX-License-Identifier: Apache-2.0 OR MIT
// c2strsimd — micro-noyaux de manipulation de chaînes et d'UTF-8, forme vectorielle AVX2.
//
// Chaque entrée publique interroge le processeur par __avx2() et n'emprunte la
// marche vectorielle que si AVX2 répond présent ; sinon elle déroule la marche
// scalaire, qui reste la définition de référence du résultat. Les corps
// vectoriels portent __attribute__((target("avx2"))) : gcc les compile en AVX2
// sans imposer -mavx2 à l'unité de traduction entière, de sorte que le binaire
// reste chargeable sur une machine sans AVX2.
//
// Côté Go, sgoiter abaisse __avx2() sur archsimd.X86.AVX2() (sonde CPUID au
// vol) et produit un compagnon scalaire c2strsimd_nosimd_gen.go d'où toute
// branche vectorielle a disparu.

#include <stdint.h>
#include <stddef.h>
#include <string.h>
#include <immintrin.h>

// Sonde de jeu d'instructions. Sous gcc, __builtin_cpu_supports interroge
// CPUID au premier appel ; sous sgoiter, l'appel s'abaisse sur la sonde
// équivalente de simd/archsimd.
static int __avx2(void) {
    return __builtin_cpu_supports("avx2");
}

// ---------------------------------------------------------------- memchr ----

// Recherche d'octet sentinelle, marche scalaire.
static int64_t c2_memchr_scalar(const uint8_t *src, uint64_t len, uint8_t target) {
    uint64_t i;
    for (i = 0; i < len; ++i) {
        if (src[i] == target) {
            return (int64_t)i;
        }
    }
    return -1;
}

// Recherche d'octet sentinelle, 32 octets par tour : diffusion de la cible,
// égalité par voie (VPCMPEQB), bitmap des voies (VPMOVMSKB), puis index du
// premier bit à un.
__attribute__((target("avx2")))
static int64_t c2_memchr_avx2(const uint8_t *src, uint64_t len, uint8_t target) {
    __m256i needle;
    __m256i chunk;
    __m256i eq;
    uint64_t i;
    uint64_t hit;
    uint32_t mask;
    int found;

    needle = _mm256_set1_epi8((char)target);
    found = 0;
    hit = 0;
    i = 0;
    while (i + 32 <= len) {
        chunk = _mm256_loadu_si256((const __m256i *)(src + i));
        eq = _mm256_cmpeq_epi8(chunk, needle);
        mask = (uint32_t)_mm256_movemask_epi8(eq);
        if (mask != 0) {
            hit = i + (uint64_t)__builtin_ctz(mask);
            found = 1;
            i = len;
        } else {
            i += 32;
        }
    }
    _mm256_zeroupper();
    if (found != 0) {
        return (int64_t)hit;
    }
    while (i < len) {
        if (src[i] == target) {
            return (int64_t)i;
        }
        i += 1;
    }
    return -1;
}

int64_t c2_memchr(const uint8_t *src, uint64_t len, uint8_t target) {
    if (__avx2()) {
        return c2_memchr_avx2(src, len, target);
    }
    return c2_memchr_scalar(src, len, target);
}

// ---------------------------------------------------------------- strpos ----

// Recherche de sous-chaîne à sentinelle double (premier et dernier octets),
// marche scalaire.
static int64_t c2_strpos_scalar(
    const uint8_t *haystack, uint64_t h_len,
    const uint8_t *needle, uint64_t n_len
) {
    uint8_t first;
    uint8_t last;
    uint64_t last_off;
    uint64_t end;
    uint64_t i;
    uint64_t k;

    if (n_len == 0) return 0;
    if (h_len < n_len) return -1;
    if (n_len == 1) return c2_memchr_scalar(haystack, h_len, needle[0]);

    first = needle[0];
    last_off = n_len - 1;
    last = needle[last_off];
    end = h_len - last_off;

    for (i = 0; i < end; ++i) {
        if (haystack[i] == first && haystack[i + last_off] == last) {
            k = 1;
            while (k < last_off && haystack[i + k] == needle[k]) {
                k += 1;
            }
            if (k == last_off) {
                return (int64_t)i;
            }
        }
    }
    return -1;
}

// Recherche de sous-chaîne à sentinelle double AVX2, 32 candidats par tour :
// deux chargements décalés de last_off = n_len - 1 octets comparent en
// parallèle le premier et le dernier octet de l'aiguille (VPCMPEQB × 2,
// VPAND, VPMOVMSKB). Pour chaque bit à un du masque, seuls les octets
// intérieurs [1 .. n_len-2] sont vérifiés (aucun pour n_len == 2).
__attribute__((target("avx2")))
static int64_t c2_strpos_avx2(
    const uint8_t *haystack, uint64_t h_len,
    const uint8_t *needle, uint64_t n_len
) {
    __m256i v_first;
    __m256i v_last;
    __m256i chunk_first;
    __m256i chunk_last;
    __m256i eq_first;
    __m256i eq_last;
    __m256i both;
    uint8_t first;
    uint8_t last;
    uint64_t last_off;
    uint64_t end;
    uint64_t i;
    uint64_t cand;
    uint64_t k;
    uint64_t hit;
    uint32_t mask;
    int found;

    if (n_len == 0) return 0;
    if (h_len < n_len) return -1;
    if (n_len == 1) return c2_memchr_avx2(haystack, h_len, needle[0]);

    first = needle[0];
    last_off = n_len - 1;
    last = needle[last_off];
    end = h_len - last_off;

    v_first = _mm256_set1_epi8((char)first);
    v_last = _mm256_set1_epi8((char)last);
    found = 0;
    hit = 0;
    i = 0;

    while (i + 32 <= end) {
        chunk_first = _mm256_loadu_si256((const __m256i *)(haystack + i));
        chunk_last = _mm256_loadu_si256((const __m256i *)(haystack + i + last_off));
        eq_first = _mm256_cmpeq_epi8(chunk_first, v_first);
        eq_last = _mm256_cmpeq_epi8(chunk_last, v_last);
        both = _mm256_and_si256(eq_first, eq_last);
        mask = (uint32_t)_mm256_movemask_epi8(both);
        while (mask != 0) {
            cand = i + (uint64_t)__builtin_ctz(mask);
            k = 1;
            while (k < last_off && haystack[cand + k] == needle[k]) {
                k += 1;
            }
            if (k == last_off) {
                hit = cand;
                found = 1;
                mask = 0;
                i = end;
            } else {
                mask &= mask - 1;
            }
        }
        if (found == 0) {
            i += 32;
        }
    }
    _mm256_zeroupper();
    if (found != 0) {
        return (int64_t)hit;
    }
    while (i < end) {
        if (haystack[i] == first && haystack[i + last_off] == last) {
            k = 1;
            while (k < last_off && haystack[i + k] == needle[k]) {
                k += 1;
            }
            if (k == last_off) {
                return (int64_t)i;
            }
        }
        i += 1;
    }
    return -1;
}

int64_t c2_strpos(
    const uint8_t *haystack, uint64_t h_len,
    const uint8_t *needle, uint64_t n_len
) {
    if (__avx2()) {
        return c2_strpos_avx2(haystack, h_len, needle, n_len);
    }
    return c2_strpos_scalar(haystack, h_len, needle, n_len);
}

// ------------------------------------------------------------- strtoupper ---

// Conversion majuscule ASCII sans branchement, marche scalaire.
static void c2_strtoupper_scalar(uint8_t *dst, const uint8_t *src, uint64_t len) {
    uint64_t i;
    for (i = 0; i < len; ++i) {
        uint8_t c = src[i];
        uint8_t is_lower = (uint8_t)((uint8_t)(c - 'a') <= 25);
        dst[i] = c - (is_lower * 0x20);
    }
}

// Conversion majuscule ASCII, 32 octets par tour. AVX2 n'offre aucune
// comparaison d'octets NON SIGNÉS : l'appartenance à ['a'..'z'] s'écrit donc
// min_epu8(c - 'a', 25) == c - 'a' (VPMINUB puis VPCMPEQB), vrai exactement
// quand (uint8_t)(c - 'a') <= 25, c'est-à-dire le prédicat scalaire ci-dessus.
__attribute__((target("avx2")))
static void c2_strtoupper_avx2(uint8_t *dst, const uint8_t *src, uint64_t len) {
    __m256i base;
    __m256i lim;
    __m256i flip;
    __m256i chunk;
    __m256i shifted;
    __m256i is_lower;
    __m256i delta;
    __m256i res;
    uint64_t i;

    base = _mm256_set1_epi8((char)'a');
    lim = _mm256_set1_epi8((char)25);
    flip = _mm256_set1_epi8((char)0x20);
    i = 0;
    while (i + 32 <= len) {
        chunk = _mm256_loadu_si256((const __m256i *)(src + i));
        shifted = _mm256_sub_epi8(chunk, base);
        is_lower = _mm256_cmpeq_epi8(_mm256_min_epu8(shifted, lim), shifted);
        delta = _mm256_and_si256(is_lower, flip);
        res = _mm256_sub_epi8(chunk, delta);
        _mm256_storeu_si256((__m256i *)(dst + i), res);
        i += 32;
    }
    _mm256_zeroupper();
    while (i < len) {
        uint8_t c = src[i];
        uint8_t is_low = (uint8_t)((uint8_t)(c - 'a') <= 25);
        dst[i] = c - (is_low * 0x20);
        i += 1;
    }
}

void c2_strtoupper(uint8_t *dst, const uint8_t *src, uint64_t len) {
    if (__avx2()) {
        c2_strtoupper_avx2(dst, src, len);
        return;
    }
    c2_strtoupper_scalar(dst, src, len);
}

// ------------------------------------------------------------- strtolower ---

// Conversion minuscule ASCII sans branchement, marche scalaire.
static void c2_strtolower_scalar(uint8_t *dst, const uint8_t *src, uint64_t len) {
    uint64_t i;
    for (i = 0; i < len; ++i) {
        uint8_t c = src[i];
        uint8_t is_upper = (uint8_t)((uint8_t)(c - 'A') <= 25);
        dst[i] = c + (is_upper * 0x20);
    }
}

// Conversion minuscule ASCII, 32 octets par tour, même test de plage non signé
// que c2_strtoupper_avx2 mais ancré sur 'A'.
__attribute__((target("avx2")))
static void c2_strtolower_avx2(uint8_t *dst, const uint8_t *src, uint64_t len) {
    __m256i base;
    __m256i lim;
    __m256i flip;
    __m256i chunk;
    __m256i shifted;
    __m256i is_upper;
    __m256i delta;
    __m256i res;
    uint64_t i;

    base = _mm256_set1_epi8((char)'A');
    lim = _mm256_set1_epi8((char)25);
    flip = _mm256_set1_epi8((char)0x20);
    i = 0;
    while (i + 32 <= len) {
        chunk = _mm256_loadu_si256((const __m256i *)(src + i));
        shifted = _mm256_sub_epi8(chunk, base);
        is_upper = _mm256_cmpeq_epi8(_mm256_min_epu8(shifted, lim), shifted);
        delta = _mm256_and_si256(is_upper, flip);
        res = _mm256_add_epi8(chunk, delta);
        _mm256_storeu_si256((__m256i *)(dst + i), res);
        i += 32;
    }
    _mm256_zeroupper();
    while (i < len) {
        uint8_t c = src[i];
        uint8_t is_up = (uint8_t)((uint8_t)(c - 'A') <= 25);
        dst[i] = c + (is_up * 0x20);
        i += 1;
    }
}

void c2_strtolower(uint8_t *dst, const uint8_t *src, uint64_t len) {
    if (__avx2()) {
        c2_strtolower_avx2(dst, src, len);
        return;
    }
    c2_strtolower_scalar(dst, src, len);
}

// ---------------------------------------------------------- mb_strlen_utf8 --

// Comptage de points de code UTF-8, marche scalaire. En UTF-8, tout octet qui
// n'est pas un octet de continuation (10xxxxxx) démarre un point de code.
static uint64_t c2_utf8_count_scalar(const uint8_t *src, uint64_t byte_len) {
    uint64_t count = 0;
    uint64_t i;
    for (i = 0; i < byte_len; ++i) {
        if ((src[i] & 0xC0) != 0x80) {
            count++;
        }
    }
    return count;
}

// Comptage de points de code UTF-8, 32 octets par tour : les octets de
// continuation se repèrent par (c & 0xC0) == 0x80, leur bitmap se compte par
// popcount, et le tour apporte 32 moins ce compte.
__attribute__((target("avx2")))
static uint64_t c2_utf8_count_avx2(const uint8_t *src, uint64_t byte_len) {
    __m256i cont_mask;
    __m256i cont_pat;
    __m256i chunk;
    __m256i eq;
    uint64_t count;
    uint64_t i;
    uint32_t bits;

    cont_mask = _mm256_set1_epi8((char)0xC0);
    cont_pat = _mm256_set1_epi8((char)0x80);
    count = 0;
    i = 0;
    while (i + 32 <= byte_len) {
        chunk = _mm256_loadu_si256((const __m256i *)(src + i));
        eq = _mm256_cmpeq_epi8(_mm256_and_si256(chunk, cont_mask), cont_pat);
        bits = (uint32_t)_mm256_movemask_epi8(eq);
        count += 32 - (uint64_t)__builtin_popcount(bits);
        i += 32;
    }
    _mm256_zeroupper();
    while (i < byte_len) {
        if ((src[i] & 0xC0) != 0x80) {
            count += 1;
        }
        i += 1;
    }
    return count;
}

uint64_t c2_utf8_count(const uint8_t *src, uint64_t byte_len) {
    if (__avx2()) {
        return c2_utf8_count_avx2(src, byte_len);
    }
    return c2_utf8_count_scalar(src, byte_len);
}

// -------------------------------------------------------------- trim -------

// Détection de délimiteur ltrim par masque de bits 256 bits (table 32 octets).
// La table est indexée par l'octet courant : une consultation par octet, sans
// forme vectorielle utile à cette largeur.
uint64_t c2_ltrim_offset(const uint8_t *src, uint64_t len, const uint8_t *cutset_bitmap_32b) {
    uint64_t i;
    for (i = 0; i < len; ++i) {
        uint8_t c = src[i];
        uint8_t in_cutset = (cutset_bitmap_32b[c >> 3] >> (c & 7)) & 1;
        if (!in_cutset) {
            return i;
        }
    }
    return len;
}

// Détection de délimiteur rtrim, même table, balayage par la fin.
uint64_t c2_rtrim_len(const uint8_t *src, uint64_t len, const uint8_t *cutset_bitmap_32b) {
    while (len > 0) {
        uint8_t c = src[len - 1];
        uint8_t in_cutset = (cutset_bitmap_32b[c >> 3] >> (c & 7)) & 1;
        if (!in_cutset) {
            break;
        }
        len--;
    }
    return len;
}

static int64_t c2_utf8_validate_from(const uint8_t *src, uint64_t len, uint64_t i) {
    while (i < len) {
        uint8_t c = src[i];
        uint64_t need;
        uint8_t min2;
        uint8_t max2;
        uint64_t k;
        if (c <= 0x7F) {
            i += 1;
            continue;
        }
        if (c >= 0xC2 && c <= 0xDF) {
            need = 2;
            min2 = 0x80;
            max2 = 0xBF;
        } else if (c == 0xE0) {
            need = 3;
            min2 = 0xA0;
            max2 = 0xBF;
        } else if ((c >= 0xE1 && c <= 0xEC) || (c >= 0xEE && c <= 0xEF)) {
            need = 3;
            min2 = 0x80;
            max2 = 0xBF;
        } else if (c == 0xED) {
            need = 3;
            min2 = 0x80;
            max2 = 0x9F;
        } else if (c == 0xF0) {
            need = 4;
            min2 = 0x90;
            max2 = 0xBF;
        } else if (c >= 0xF1 && c <= 0xF3) {
            need = 4;
            min2 = 0x80;
            max2 = 0xBF;
        } else if (c == 0xF4) {
            need = 4;
            min2 = 0x80;
            max2 = 0x8F;
        } else {
            return 0;
        }
        if (i + need > len) {
            return 0;
        }
        if (src[i + 1] < min2 || src[i + 1] > max2) {
            return 0;
        }
        k = 2;
        while (k < need) {
            if (src[i + k] < 0x80 || src[i + k] > 0xBF) {
                return 0;
            }
            k += 1;
        }
        i += need;
    }
    return 1;
}

static int64_t c2_utf8_validate_scalar(const uint8_t *src, uint64_t len) {
    return c2_utf8_validate_from(src, len, 0);
}

__attribute__((target("avx2")))
static int64_t c2_utf8_validate_avx2(const uint8_t *src, uint64_t len) {
    __m256i chunk;
    uint64_t i;
    uint32_t mask;
    i = 0;
    while (i + 32 <= len) {
        chunk = _mm256_loadu_si256((const __m256i *)(src + i));
        mask = (uint32_t)_mm256_movemask_epi8(chunk);
        if (mask != 0) {
            _mm256_zeroupper();
            return c2_utf8_validate_from(src, len, i);
        }
        i += 32;
    }
    _mm256_zeroupper();
    return c2_utf8_validate_from(src, len, i);
}

int64_t c2_utf8_validate(const uint8_t *src, uint64_t len) {
    if (__avx2()) {
        return c2_utf8_validate_avx2(src, len);
    }
    return c2_utf8_validate_scalar(src, len);
}
