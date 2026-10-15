/* SPDX-License-Identifier: Apache-2.0 OR MIT */
#include <stdint.h>
#include <string.h>
#include <immintrin.h>

#include "php_kw_resolve.h"

static int __avx2(void) {
    return __builtin_cpu_supports("avx2");
}

/* Masque des m premiers octets d'un mot (0 <= m <= 8), sans décalage de 64. */
static inline uint64_t kw_keep(uint32_t m) {
    return (~0ull >> (32 - 4 * m)) >> (32 - 4 * m);
}

/* Repli ASCII des huit octets de w : chaque octet de 'A' à 'Z' reçoit fold
 * (0x20 ou 0), les octets de 0x80 à 0xFF sont laissés intacts. */
static inline uint64_t kw_fold8(uint64_t w, uint8_t fold) {
    uint64_t low7 = w & 0x7F7F7F7F7F7F7F7Full;
    uint64_t ge_a = low7 + 0x3F3F3F3F3F3F3F3Full;
    uint64_t gt_z = low7 + 0x2525252525252525ull;
    uint64_t up = ge_a & ~gt_z & ~w & 0x8080808080808080ull;
    return w | ((up >> 2) & (0x0101010101010101ull * fold));
}

/* Mot de hachage : les min(n, 8) premiers octets repliés, longueur en tête. */
static inline uint64_t kw_hash_word(uint64_t w0, uint32_t n) {
    uint32_t over = 0u - (uint32_t)(n > 8);
    uint32_t m = (n & ~over) | (8u & over);
    return (w0 & kw_keep(m)) ^ ((uint64_t)n << 56);
}

static inline uint32_t kw_slot(const c2_kw_table_t *t, uint64_t w) {
    uint32_t b = (uint32_t)((w * t->m1) >> 58);
    return (uint32_t)(((w * t->m2) >> 56) ^ t->disp[b]) & (C2_KW_SLOTS - 1);
}

uint16_t c2_kw_resolve_scalar(const c2_kw_table_t *t, const uint8_t *s, uint32_t n, uint16_t ident) {
    uint32_t nn = n & (0u - (uint32_t)(n <= C2_KW_MAXLEN));
    uint64_t w0 = 0, w1 = 0, k0 = 0, k1 = 0;
    memcpy(&w0, s, 8);
    memcpy(&w1, s + 8, 8);
    w0 = kw_fold8(w0, t->fold);
    w1 = kw_fold8(w1, t->fold);
    uint32_t slot = kw_slot(t, kw_hash_word(w0, nn));
    memcpy(&k0, t->key + slot * 16, 8);
    memcpy(&k1, t->key + slot * 16 + 8, 8);
    uint32_t over = 0u - (uint32_t)(nn > 8);
    uint32_t m0 = (nn & ~over) | (8u & over);
    uint64_t diff = ((w0 ^ k0) & kw_keep(m0)) | ((w1 ^ k1) & kw_keep(nn - m0));
    uint32_t eq = (uint32_t)(diff == 0) & (uint32_t)(t->len[slot] == nn) & (uint32_t)(nn != 0);
    uint32_t m = 0u - eq;
    return (uint16_t)((t->tok[slot] & m) | (ident & ~m));
}

__attribute__((target("avx2")))
uint16_t c2_kw_resolve_avx2(const c2_kw_table_t *t, const uint8_t *s, uint32_t n, uint16_t ident) {
    uint32_t nn = n & (0u - (uint32_t)(n <= C2_KW_MAXLEN));
    uint64_t w0 = 0;
    memcpy(&w0, s, 8);
    uint32_t slot = kw_slot(t, kw_hash_word(kw_fold8(w0, t->fold), nn));
    __m128i in   = _mm_loadu_si128((const __m128i *)s);
    __m128i up   = _mm_and_si128(_mm_cmpgt_epi8(in, _mm_set1_epi8('A' - 1)),
                                 _mm_cmpgt_epi8(_mm_set1_epi8('Z' + 1), in));
    in = _mm_or_si128(in, _mm_and_si128(up, _mm_set1_epi8((char)t->fold)));
    __m128i iota = _mm_setr_epi8(0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15);
    __m128i live = _mm_cmpgt_epi8(_mm_set1_epi8((char)nn), iota);
    __m128i key  = _mm_loadu_si128((const __m128i *)(t->key + slot * 16));
    __m128i diff = _mm_and_si128(_mm_xor_si128(in, key), live);
    __m128i zero = _mm_setzero_si128();
    uint32_t is_zero = ((uint32_t)_mm_movemask_epi8(_mm_cmpeq_epi8(diff, zero)) == 0xFFFFu);
    uint32_t eq  = is_zero & (uint32_t)(t->len[slot] == nn) & (uint32_t)(nn != 0);
    uint32_t m   = 0u - eq;
    return (uint16_t)((t->tok[slot] & m) | (ident & ~m));
}

uint16_t c2_kw_resolve(const c2_kw_table_t *t, const uint8_t *s, uint32_t n, uint16_t ident) {
    if (__avx2()) {
        return c2_kw_resolve_avx2(t, s, n, ident);
    }
    return c2_kw_resolve_scalar(t, s, n, ident);
}

uint16_t c2_kw_resolve_pyramid(const c2_kw_table_t *t, const uint8_t *s, uint32_t n, uint16_t ident) {
    if (n > 15 || !((t->eh0[s[0]] >> n) & 1u)) {
        return ident;
    }
    if (__avx2()) {
        return c2_kw_resolve_avx2(t, s, n, ident);
    }
    return c2_kw_resolve_scalar(t, s, n, ident);
}
