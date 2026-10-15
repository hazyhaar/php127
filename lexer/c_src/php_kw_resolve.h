/* SPDX-License-Identifier: Apache-2.0 OR MIT */
#ifndef PHP_KW_RESOLVE_H
#define PHP_KW_RESOLVE_H

#include <stdint.h>
#include <stddef.h>

#define C2_KW_SLOTS 256
#define C2_KW_MAXLEN 16

/* Repli de casse d'une table : C2_KW_FOLD_ASCII fait comparer l'entrée après
 * passage des octets 'A'..'Z' en minuscules, les clés étant stockées repliées. */
#define C2_KW_FOLD_NONE 0x00
#define C2_KW_FOLD_ASCII 0x20

/* Table de mots-clés plate en rodata L1D (5464 octets), émise par c2kwgen. */
typedef struct {
    uint64_t m1;                /* multiplicateur hachage niveau 1 */
    uint64_t m2;                /* multiplicateur hachage niveau 2 */
    uint16_t eh0[256];          /* Échelon 0 : masque 16 bits des longueurs existantes par lettre de tête */
    uint8_t  disp[64];          /* déplacements par seau (niveau 1) */
    uint8_t  key[4096];         /* 256 clés de 16 octets complétées de 0, repliées si fold */
    uint8_t  len[256];          /* longueur de chaque clé, 0 = vide */
    uint16_t tok[256];          /* code de jeton retourné */
    uint8_t  fold;              /* C2_KW_FOLD_NONE ou C2_KW_FOLD_ASCII */
} c2_kw_table_t;

/* Résolution d'un mot-clé en temps constant O(1) sans aucun branchement conditionnel.
 * Contrat : s doit comporter au moins 16 octets lisibles (rembourrage de fin garanti). */
uint16_t c2_kw_resolve(const c2_kw_table_t *t, const uint8_t *s, uint32_t n, uint16_t ident);

/* Résolution pyramidale combinant l'Échelon 0 (rejet instantané en 1 cycle) et c2_kw_resolve_avx2 */
uint16_t c2_kw_resolve_pyramid(const c2_kw_table_t *t, const uint8_t *s, uint32_t n, uint16_t ident);

uint16_t c2_kw_resolve_scalar(const c2_kw_table_t *t, const uint8_t *s, uint32_t n, uint16_t ident);

uint16_t c2_kw_resolve_avx2(const c2_kw_table_t *t, const uint8_t *s, uint32_t n, uint16_t ident);

#endif /* PHP_KW_RESOLVE_H */
