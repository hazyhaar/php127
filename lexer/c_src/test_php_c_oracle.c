/* SPDX-License-Identifier: Apache-2.0 OR MIT */
#include <stdio.h>
#include <stdint.h>
#include <string.h>

#include "php_kw_resolve.h"
#include "php_kw_table.h"

int php_scan_label_scalar(const uint8_t *s, int pos, int end);
int php_scan_label_avx2(const uint8_t *s, int pos, int end);
int php_scan_label(const uint8_t *s, int pos, int end);

int php_scan_line_comment_scalar(const uint8_t *s, int pos, int end);
int php_scan_line_comment_avx2(const uint8_t *s, int pos, int end);
int php_scan_line_comment(const uint8_t *s, int pos, int end);

int php_scan_single_quote_scalar(const uint8_t *s, int pos, int end);
int php_scan_single_quote_avx2(const uint8_t *s, int pos, int end);
int php_scan_single_quote(const uint8_t *s, int pos, int end);

int php_scan_double_quote_scalar(const uint8_t *s, int pos, int end);
int php_scan_double_quote_avx2(const uint8_t *s, int pos, int end);
int php_scan_double_quote(const uint8_t *s, int pos, int end);

int php_scan_inline_html_scalar(const uint8_t *s, int pos, int end);
int php_scan_inline_html_avx2(const uint8_t *s, int pos, int end);
int php_scan_inline_html(const uint8_t *s, int pos, int end);

int php_scan_block_comment_scalar(const uint8_t *s, int pos, int end);
int php_scan_block_comment_avx2(const uint8_t *s, int pos, int end);
int php_scan_block_comment(const uint8_t *s, int pos, int end);

typedef struct {
    const char *name;
    const c2_kw_table_t *t;
    uint16_t ident;
} table_ref_t;

static const table_ref_t TABLES[] = {
    {"kw", &php_kw_table, 262},
    {"cast", &php_cast_table, 0},
};
#define NTABLES (sizeof(TABLES) / sizeof(TABLES[0]))

static const uint8_t FILLS[] = {0x00, 'A', 0xFF};

static const char *const ADVERSE[] = {
    "", "a", "@", "[", "`", "{", "Z", "z",
    "__halt_compiler", "__HALT_COMPILER", "__Halt_Compiler", "__halt_compile", "__halt_compilerx",
    "include", "INCLUDE", "include_once", "INCLUDE_ONCE", "include_onc", "include_once_",
    "int", "INT", "integer", "INTEGER", "boolean", "BOOLEAN", "unset", "UNSET",
    "abcdefghijklmnop", "abcdefghijklmnopq", "ABCDEFGHIJKLMNOPQRST",
};
#define NADVERSE (sizeof(ADVERSE) / sizeof(ADVERSE[0]))

static int g_avx2;
static int g_dump_kw;
static int g_dump_scan;
static uint64_t g_cases;
static int g_fail;

static uint8_t fold_byte(uint8_t c, uint8_t fold) {
    return (c >= 'A' && c <= 'Z') ? (uint8_t)(c | fold) : c;
}

static uint16_t ref_resolve(const c2_kw_table_t *t, const uint8_t *s, uint32_t n, uint16_t ident) {
    uint32_t slot, i;
    if (n == 0 || n > C2_KW_MAXLEN) {
        return ident;
    }
    for (slot = 0; slot < C2_KW_SLOTS; slot++) {
        if (t->len[slot] != n) continue;
        for (i = 0; i < n; i++) {
            if (fold_byte(s[i], t->fold) != t->key[slot * 16 + i]) break;
        }
        if (i == n) return t->tok[slot];
    }
    return ident;
}

static void run_kw_case(const table_ref_t *tr, const uint8_t *word, uint32_t n) {
    uint8_t buf[32];
    size_t fi;
    uint32_t i;
    for (fi = 0; fi < sizeof(FILLS); fi++) {
        memset(buf, FILLS[fi], sizeof(buf));
        memcpy(buf, word, n);
        uint16_t want = ref_resolve(tr->t, buf, n, tr->ident);
        uint16_t want_pyr = n <= 15 ? want : tr->ident;
        uint16_t sc = c2_kw_resolve_scalar(tr->t, buf, n, tr->ident);
        uint16_t av = g_avx2 ? c2_kw_resolve_avx2(tr->t, buf, n, tr->ident) : want;
        uint16_t rs = c2_kw_resolve(tr->t, buf, n, tr->ident);
        uint16_t py = c2_kw_resolve_pyramid(tr->t, buf, n, tr->ident);
        g_cases++;
        if (sc != want || av != want || rs != want || py != want_pyr) {
            printf("FAIL kw table=%s fill=%02x n=%u scalar=%u avx2=%u resolve=%u pyramid=%u want=%u/%u\n",
                   tr->name, FILLS[fi], n, sc, av, rs, py, want, want_pyr);
            g_fail++;
        }
        if (g_dump_kw) {
            printf("%s %02x %u ", tr->name, FILLS[fi], n);
            for (i = 0; i < n; i++) printf("%02x", buf[i]);
            if (n == 0) printf("-");
            if (g_avx2) {
                printf(" %u %u %u %u\n", sc, av, rs, py);
            } else {
                printf(" %u - %u %u\n", sc, rs, py);
            }
        }
    }
}

static void run_keyword(const table_ref_t *tr, const uint8_t *key, uint32_t n) {
    uint8_t w[C2_KW_MAXLEN + 1];
    uint32_t i;
    run_kw_case(tr, key, n);
    for (i = 0; i < n; i++) {
        uint8_t c = key[i];
        w[i] = ((c | 0x20) >= 'a' && (c | 0x20) <= 'z') ? (uint8_t)(c ^ 0x20) : c;
    }
    run_kw_case(tr, w, n);
    for (i = 0; i < n; i++) w[i] = (i % 2 == 0) ? w[i] : key[i];
    run_kw_case(tr, w, n);
    run_kw_case(tr, key, n - 1);
    memcpy(w, key, n);
    w[n] = 'x';
    run_kw_case(tr, w, n + 1);
    w[n] = '_';
    run_kw_case(tr, w, n + 1);
    for (i = 0; i < n; i++) {
        memcpy(w, key, n);
        w[i] ^= 0x20;
        run_kw_case(tr, w, n);
        w[i] ^= 0x21;
        run_kw_case(tr, w, n);
    }
}

static void run_scan_buffer(const uint8_t *buf, int len) {
    int pos;
    for (pos = 0; pos <= len; pos += 7) {
        int s_lbl = php_scan_label_scalar(buf, pos, len);
        int a_lbl = g_avx2 ? php_scan_label_avx2(buf, pos, len) : s_lbl;
        int d_lbl = php_scan_label(buf, pos, len);

        int s_cmt = php_scan_line_comment_scalar(buf, pos, len);
        int a_cmt = g_avx2 ? php_scan_line_comment_avx2(buf, pos, len) : s_cmt;
        int d_cmt = php_scan_line_comment(buf, pos, len);

        int s_sq = php_scan_single_quote_scalar(buf, pos, len);
        int a_sq = g_avx2 ? php_scan_single_quote_avx2(buf, pos, len) : s_sq;
        int d_sq = php_scan_single_quote(buf, pos, len);

        int s_dq = php_scan_double_quote_scalar(buf, pos, len);
        int a_dq = g_avx2 ? php_scan_double_quote_avx2(buf, pos, len) : s_dq;
        int d_dq = php_scan_double_quote(buf, pos, len);

        int s_ht = php_scan_inline_html_scalar(buf, pos, len);
        int a_ht = g_avx2 ? php_scan_inline_html_avx2(buf, pos, len) : s_ht;
        int d_ht = php_scan_inline_html(buf, pos, len);

        int s_bc = php_scan_block_comment_scalar(buf, pos, len);
        int a_bc = g_avx2 ? php_scan_block_comment_avx2(buf, pos, len) : s_bc;
        int d_bc = php_scan_block_comment(buf, pos, len);

        g_cases += 6;
        if (s_lbl != a_lbl || s_lbl != d_lbl ||
            s_cmt != a_cmt || s_cmt != d_cmt ||
            s_sq != a_sq || s_sq != d_sq ||
            s_dq != a_dq || s_dq != d_dq ||
            s_ht != a_ht || s_ht != d_ht ||
            s_bc != a_bc || s_bc != d_bc) {
            printf("FAIL scan pos=%d len=%d\n", pos, len);
            g_fail++;
        }
        if (g_dump_scan) {
            int i;
            printf("%d %d ", pos, len);
            for (i = 0; i < len; i++) printf("%02x", buf[i]);
            if (len == 0) printf("-");
            printf(" %d %d %d %d %d %d\n", d_lbl, d_cmt, d_sq, d_dq, d_ht, d_bc);
        }
    }
}

int main(int argc, char **argv) {
    size_t ti, ai;
    uint32_t slot;
    g_avx2 = __builtin_cpu_supports("avx2") != 0;
    g_dump_kw = argc > 1 && strcmp(argv[1], "kw") == 0;
    g_dump_scan = argc > 1 && strcmp(argv[1], "scan") == 0;

    if (!g_dump_scan) {
        for (ti = 0; ti < NTABLES; ti++) {
            const table_ref_t *tr = &TABLES[ti];
            for (slot = 0; slot < C2_KW_SLOTS; slot++) {
                if (tr->t->len[slot] != 0) {
                    run_keyword(tr, tr->t->key + slot * 16, tr->t->len[slot]);
                }
            }
            for (ai = 0; ai < NADVERSE; ai++) {
                run_kw_case(tr, (const uint8_t *)ADVERSE[ai], (uint32_t)strlen(ADVERSE[ai]));
            }
        }
    }

    if (!g_dump_kw) {
        static const char *const SCAN_SAMPLES[] = {
            "",
            "identifier_1234567890_abcdefghijklmnopqrstuvwxyz_ABCDEFGHIJKLMNOPQRSTUVWXYZ_0123456789_end!",
            "ident_with_utf8_\xc3\xa9\xc3\xa0\xc3\xb9_and_more_ident_chars_beyond_32_bytes_boundary$stop",
            "comment body without newline for more than thirty two bytes until question mark ?> after",
            "comment body with crlf after forty bytes 012345678901234567890123456789\r\nnext",
            "single quoted string with many characters 012345678901234567890123456789 \\' end'",
            "double quoted string 0123456789012345678901234567890123456789 $var and {expr} and \\\" end\"",
            "inline html block <div><span>some text 0123456789012345678901234567890123456789</span></div><?php",
            "/** @param string $x * * / *** not yet closed 012345678901234567890123456789 ***/ after",
            "0123456789012345678901234567890*/boundary31_32_and_trailing_star*",
        };
        size_t si;
        for (si = 0; si < sizeof(SCAN_SAMPLES) / sizeof(SCAN_SAMPLES[0]); si++) {
            run_scan_buffer((const uint8_t *)SCAN_SAMPLES[si], (int)strlen(SCAN_SAMPLES[si]));
        }
        uint8_t all_bytes[96];
        int k;
        for (k = 0; k < 96; k++) {
            all_bytes[k] = (uint8_t)((k * 73 + 19) & 0xFF);
        }
        run_scan_buffer(all_bytes, 96);
    }

    if (!g_dump_kw && !g_dump_scan) {
        printf("php_c_oracle: %s (cases=%llu avx2=%d fail=%d)\n", g_fail ? "FAIL" : "OK",
               (unsigned long long)g_cases, g_avx2, g_fail);
    }
    return g_fail ? 1 : 0;
}
