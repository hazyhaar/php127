// Oracle de parité de utf8_scan.c : compilé par gcc -O2 avec le noyau, il imprime pour un corpus
// déterministe chaque résultat des trois entrées publiques ; TestSgoiterUTF8ScanParityVsGCC rejoue
// chaque ligne contre le Go transpilé par sgoiter.
//
// Format : « str <hex|-> » ouvre une chaîne, puis « c2b <rang> <résultat> », « b2c <décalage> <résultat> »
// et « cut <depuis> <position> <résultat> » s'y rapportent.

#include <stdint.h>
#include <stdio.h>

uint64_t c2_utf8_char_to_byte(const uint8_t *s, uint64_t len, uint64_t char_idx);
uint64_t c2_utf8_byte_to_char(const uint8_t *s, uint64_t byte_offset);
uint64_t c2_utf8_cut_floor(const uint8_t *s, uint64_t from, uint64_t pos);

static uint32_t rng = 0x9e3779b9u;

static uint32_t next(void) {
    rng ^= rng << 13;
    rng ^= rng >> 17;
    rng ^= rng << 5;
    return rng;
}

// Octets tirés dans chaque classe de l'automate UTF-8 : ASCII, continuations, têtes valides et invalides.
static const uint8_t classes[] = {
    'a', 'Z', 0x00, 0x7f, 0x80, 0x8f, 0x90, 0x9f, 0xa0, 0xbf, 0xc0, 0xc1, 0xc2, 0xdf,
    0xe0, 0xe1, 0xec, 0xed, 0xee, 0xef, 0xf0, 0xf1, 0xf3, 0xf4, 0xf5, 0xf8, 0xfe, 0xff,
};

static const char *seqs[] = {"\xc3\xa9", "\xe4\xbd\xa0", "\xf0\x9f\x99\x82", "\xe2\x82", "\xed\xa0\x80"};

static uint64_t fill(uint8_t *dst, uint64_t n, int ascii_heavy) {
    uint64_t i = 0;
    while (i < n) {
        uint32_t r = next();
        if (ascii_heavy && r % 23 != 0) {
            dst[i++] = (uint8_t)('a' + r % 26);
        } else if (r % 5 == 0) {
            const char *q = seqs[(r >> 8) % 5];
            while (*q && i < n) {
                dst[i++] = (uint8_t)*q++;
            }
        } else {
            dst[i++] = classes[(r >> 8) % sizeof(classes)];
        }
    }
    return n;
}

static void dump(const uint8_t *s, uint64_t n) {
    uint64_t i;
    uint64_t k;
    uint64_t pos;
    fputs("str ", stdout);
    if (n == 0) {
        fputs("-", stdout);
    }
    for (i = 0; i < n; i++) {
        printf("%02x", s[i]);
    }
    fputs("\n", stdout);
    for (k = 0; k <= n + 1; k++) {
        printf("c2b %llu %llu\n", (unsigned long long)k, (unsigned long long)c2_utf8_char_to_byte(s, n, k));
    }
    for (i = 0; i <= n; i++) {
        printf("b2c %llu %llu\n", (unsigned long long)i, (unsigned long long)c2_utf8_byte_to_char(s, i));
    }
    for (pos = 0; pos <= n; pos++) {
        uint64_t start = c2_utf8_cut_floor(s, 0, pos);
        printf("cut 0 %llu %llu\n", (unsigned long long)pos, (unsigned long long)start);
        for (k = start; k < n; k += 1 + (n > 64 ? 7 : 0)) {
            printf("cut %llu %llu %llu\n", (unsigned long long)start, (unsigned long long)k,
                   (unsigned long long)c2_utf8_cut_floor(s, start, k));
        }
    }
}

int main(void) {
    uint8_t buf[320];
    uint64_t n;
    int t;
    for (t = 0; t < 400; t++) {
        n = fill(buf, (uint64_t)(t % 97), 0);
        dump(buf, n);
    }
    for (t = 0; t < 40; t++) {
        n = fill(buf, 96 + (uint64_t)(next() % 224), 1);
        dump(buf, n);
    }
    puts("utf8_scan_oracle: OK");
    return 0;
}
