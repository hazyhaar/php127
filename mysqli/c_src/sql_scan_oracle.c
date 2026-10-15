// SPDX-License-Identifier: Apache-2.0 OR MIT
// sql_scan_oracle — oracle gcc -O2 des noyaux de sql_scan.c.
//
// Chaque ligne d'entrée porte « hex pos end » (« - » pour une entrée vide) ;
// chaque ligne de sortie porte, pour skip_spaces, scan_word puis find_escape,
// les résultats des marches scalaire, AVX2 et de l'entrée publique.

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

uint64_t c2_sql_skip_spaces_scalar(const uint8_t *src, uint64_t pos, uint64_t end);
uint64_t c2_sql_skip_spaces_avx2(const uint8_t *src, uint64_t pos, uint64_t end);
uint64_t c2_sql_skip_spaces(const uint8_t *src, uint64_t pos, uint64_t end);
uint64_t c2_sql_scan_word_scalar(const uint8_t *src, uint64_t pos, uint64_t end);
uint64_t c2_sql_scan_word_avx2(const uint8_t *src, uint64_t pos, uint64_t end);
uint64_t c2_sql_scan_word(const uint8_t *src, uint64_t pos, uint64_t end);
uint64_t c2_sql_find_escape_scalar(const uint8_t *src, uint64_t pos, uint64_t end);
uint64_t c2_sql_find_escape_avx2(const uint8_t *src, uint64_t pos, uint64_t end);
uint64_t c2_sql_find_escape(const uint8_t *src, uint64_t pos, uint64_t end);

static int hexval(int c) {
    if (c >= '0' && c <= '9') {
        return c - '0';
    }
    if (c >= 'a' && c <= 'f') {
        return c - 'a' + 10;
    }
    return -1;
}

int main(void) {
    static char line[1 << 16];
    static uint8_t buf[1 << 15];
    static char hex[1 << 16];
    unsigned long long pos;
    unsigned long long end;
    unsigned long cases = 0;

    if (!__builtin_cpu_supports("avx2")) {
        fputs("sql_scan_oracle: AVX2 absent\n", stderr);
        return 2;
    }
    while (fgets(line, sizeof line, stdin) != NULL) {
        size_t n = 0;
        size_t i;
        if (sscanf(line, "%65535s %llu %llu", hex, &pos, &end) != 3) {
            fprintf(stderr, "sql_scan_oracle: ligne illisible %s", line);
            return 1;
        }
        if (strcmp(hex, "-") != 0) {
            size_t h = strlen(hex);
            if (h % 2 != 0 || h / 2 > sizeof buf) {
                fprintf(stderr, "sql_scan_oracle: hexadécimal invalide\n");
                return 1;
            }
            for (i = 0; i < h; i += 2) {
                int hi = hexval(hex[i]);
                int lo = hexval(hex[i + 1]);
                if (hi < 0 || lo < 0) {
                    fprintf(stderr, "sql_scan_oracle: hexadécimal invalide\n");
                    return 1;
                }
                buf[n++] = (uint8_t)(hi * 16 + lo);
            }
        }
        if (pos > end || end > n) {
            fprintf(stderr, "sql_scan_oracle: bornes invalides %llu %llu %zu\n", pos, end, n);
            return 1;
        }
        printf("%llu %llu %llu %llu %llu %llu %llu %llu %llu\n",
               (unsigned long long)c2_sql_skip_spaces_scalar(buf, pos, end),
               (unsigned long long)c2_sql_skip_spaces_avx2(buf, pos, end),
               (unsigned long long)c2_sql_skip_spaces(buf, pos, end),
               (unsigned long long)c2_sql_scan_word_scalar(buf, pos, end),
               (unsigned long long)c2_sql_scan_word_avx2(buf, pos, end),
               (unsigned long long)c2_sql_scan_word(buf, pos, end),
               (unsigned long long)c2_sql_find_escape_scalar(buf, pos, end),
               (unsigned long long)c2_sql_find_escape_avx2(buf, pos, end),
               (unsigned long long)c2_sql_find_escape(buf, pos, end));
        cases++;
    }
    fprintf(stderr, "sql_scan_oracle: %lu cas\n", cases);
    return 0;
}
