#include <stdint.h>
#include <stdio.h>
#include <string.h>

int64_t c2_memchr(const uint8_t *src, uint64_t len, uint8_t target);
int64_t c2_strpos(const uint8_t *haystack, uint64_t h_len, const uint8_t *needle, uint64_t n_len);
void c2_strtoupper(uint8_t *dst, const uint8_t *src, uint64_t len);
void c2_strtolower(uint8_t *dst, const uint8_t *src, uint64_t len);
int64_t c2_utf8_validate(const uint8_t *src, uint64_t len);
uint64_t c2_utf8_count(const uint8_t *src, uint64_t byte_len);

static void hexout(const uint8_t *p, uint64_t n) {
    uint64_t i;
    if (n == 0) {
        fputs("-", stdout);
        return;
    }
    for (i = 0; i < n; i++) {
        printf("%02x", p[i]);
    }
}

static void fill(uint8_t *dst, uint64_t n, uint32_t seed) {
    uint64_t i;
    uint32_t x = seed ? seed : 1u;
    for (i = 0; i < n; i++) {
        x = x * 1664525u + 1013904223u;
        dst[i] = (uint8_t)(x >> 24);
    }
}

int main(void) {
    uint8_t src[96];
    uint8_t dst[96];
    uint8_t needle[8];
    uint64_t n;
    uint64_t k;
    unsigned t;
    static const char *texts[] = {
        "", "a", "abc", "Hello", "aaa", "xyzxyz", "\x00\x01", "caf\xc3\xa9",
        "\x80", "\xc2\x80", "\xc0\x80", "\xed\xa0\x80", "\xf4\x8f\xbf\xbf",
        "\xf4\x90\x80\x80", "\xe2\x82\xac", "ok\xff",
    };
    unsigned ti;

    for (n = 0; n <= 64; n++) {
        fill(src, n, (uint32_t)(n * 17u + 3u));
        for (t = 0; t < 4; t++) {
            uint8_t target = (uint8_t)(t == 0 ? 0 : t == 1 ? 0xff : t == 2 && n ? src[n / 2] : (uint8_t)('A' + t));
            int64_t hit = c2_memchr(src, n, target);
            fputs("memchr ", stdout);
            hexout(src, n);
            printf(" %02x %lld\n", target, (long long)hit);
        }
    }

    for (n = 0; n <= 80; n++) {
        fill(src, n, (uint32_t)(1000u + n));
        for (k = 0; k <= 5 && k <= n; k++) {
            if (k == 0) {
                needle[0] = 0;
            } else if (n == 0) {
                continue;
            } else {
                memcpy(needle, src + (n - k) / 2, k);
            }
            fputs("strpos ", stdout);
            hexout(src, n);
            fputc(' ', stdout);
            hexout(needle, k);
            printf(" %lld\n", (long long)c2_strpos(src, n, needle, k));
        }
        if (n >= 4) {
            for (k = 0; k + 3 < n; k += 5) {
                src[k] = 'A';
                src[k + 1] = 'x';
                src[k + 2] = 'y';
                src[k + 3] = 'B';
            }
            if (n >= 40) {
                src[n - 4] = 'A';
                src[n - 3] = '1';
                src[n - 2] = '2';
                src[n - 1] = 'B';
            }
            needle[0] = 'A';
            needle[1] = '1';
            needle[2] = '2';
            needle[3] = 'B';
            fputs("strpos ", stdout);
            hexout(src, n);
            fputc(' ', stdout);
            hexout(needle, 4);
            printf(" %lld\n", (long long)c2_strpos(src, n, needle, 4));
        }
        if (n >= 3) {
            needle[0] = 0x01;
            needle[1] = 0x02;
            needle[2] = 0x03;
            fputs("strpos ", stdout);
            hexout(src, n);
            fputs(" 010203 ", stdout);
            printf("%lld\n", (long long)c2_strpos(src, n, needle, 3));
        }
    }

    for (n = 0; n <= 80; n++) {
        fill(src, n, (uint32_t)(n * 9u + 7u));
        for (k = 0; k < n; k++) {
            if ((k % 5u) == 0) {
                src[k] = (uint8_t)('a' + (k % 26u));
            } else if ((k % 5u) == 1) {
                src[k] = (uint8_t)('A' + (k % 26u));
            }
        }
        c2_strtoupper(dst, src, n);
        fputs("upper ", stdout);
        hexout(src, n);
        fputc(' ', stdout);
        hexout(dst, n);
        fputc('\n', stdout);
        c2_strtolower(dst, src, n);
        fputs("lower ", stdout);
        hexout(src, n);
        fputc(' ', stdout);
        hexout(dst, n);
        fputc('\n', stdout);
        fputs("utf8v ", stdout);
        hexout(src, n);
        printf(" %lld\n", (long long)c2_utf8_validate(src, n));
        fputs("utf8c ", stdout);
        hexout(src, n);
        printf(" %llu\n", (unsigned long long)c2_utf8_count(src, n));
    }

    for (ti = 0; ti < sizeof(texts) / sizeof(texts[0]); ti++) {
        n = (uint64_t)strlen(texts[ti]);
        memcpy(src, texts[ti], n);
        fputs("utf8v ", stdout);
        hexout(src, n);
        printf(" %lld\n", (long long)c2_utf8_validate(src, n));
        fputs("utf8c ", stdout);
        hexout(src, n);
        printf(" %llu\n", (unsigned long long)c2_utf8_count(src, n));
        c2_strtoupper(dst, src, n);
        fputs("upper ", stdout);
        hexout(src, n);
        fputc(' ', stdout);
        hexout(dst, n);
        fputc('\n', stdout);
    }
    fputs("php_strsimd_oracle: OK\n", stdout);
    return 0;
}
