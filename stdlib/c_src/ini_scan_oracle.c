// Oracle de parité de ini_scan.c : compilé par gcc -O2 avec ini_scan.c, il imprime pour chaque
// entrée le résultat des entrées publiques ; TestSgoiterINIScanParityVsGCC rejoue chaque ligne
// sur le Go émis par sgoiter. Format : « op hex pos end want » pour les balayages, « kw hex want »
// pour les mots-clés ; hex vaut « - » pour une entrée vide.

#include <stdint.h>
#include <stdio.h>
#include <string.h>

uint64_t c2_ini_skip_hspace(const uint8_t *src, uint64_t pos, uint64_t end);
uint64_t c2_ini_find_eol(const uint8_t *src, uint64_t pos, uint64_t end);
uint64_t c2_ini_scan_key(const uint8_t *src, uint64_t pos, uint64_t end);
int64_t c2_ini_kw_code(const uint8_t *src, uint64_t len);

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

static uint32_t lcg(uint32_t *x) {
    *x = *x * 1664525u + 1013904223u;
    return *x >> 24;
}

// Alphabet biaisé vers les octets qui arrêtent ou prolongent les trois balayages.
static const uint8_t alphabet[] = " \t\t  \n\r=;[]!\"$&()^{|}~kKeyY09._-/\x00\x80\xff";

static void fill(uint8_t *dst, uint64_t n, uint32_t seed, uint32_t density) {
    uint64_t i;
    uint32_t x = seed ? seed : 1u;
    for (i = 0; i < n; i++) {
        uint32_t r = lcg(&x);
        if (r % 16u < density) {
            dst[i] = alphabet[lcg(&x) % (uint32_t)(sizeof(alphabet) - 1)];
        } else if (r % 2u == 0) {
            dst[i] = (uint8_t)(r & 1u ? ' ' : 'a' + r % 26u);
        } else {
            dst[i] = (uint8_t)lcg(&x);
        }
    }
}

static void scans(const uint8_t *src, uint64_t n, uint64_t pos) {
    printf("hsp ");
    hexout(src, n);
    printf(" %llu %llu %llu\n", (unsigned long long)pos, (unsigned long long)n,
           (unsigned long long)c2_ini_skip_hspace(src, pos, n));
    printf("eol ");
    hexout(src, n);
    printf(" %llu %llu %llu\n", (unsigned long long)pos, (unsigned long long)n,
           (unsigned long long)c2_ini_find_eol(src, pos, n));
    printf("key ");
    hexout(src, n);
    printf(" %llu %llu %llu\n", (unsigned long long)pos, (unsigned long long)n,
           (unsigned long long)c2_ini_scan_key(src, pos, n));
}

static void kw(const uint8_t *src, uint64_t n) {
    printf("kw ");
    hexout(src, n);
    printf(" %lld\n", (long long)c2_ini_kw_code(src, n));
}

int main(void) {
    static const char *words[] = {"true", "on", "yes", "false", "off", "no", "none", "null",
                                  "tru", "nul", "nonE", "yess", "o", "", "falsee", "n0ne", "0n"};
    uint8_t src[160];
    uint8_t w[8];
    uint64_t n;
    uint64_t k;
    uint32_t d;
    unsigned b;
    unsigned wi;
    unsigned m;

    // Longueurs 0..140 sous quatre densités, départs au début, au milieu, à un octet et à la fin.
    for (n = 0; n <= 140; n++) {
        for (d = 0; d < 16; d += 5) {
            uint64_t starts[5];
            fill(src, n, (uint32_t)(n * 31u + d * 7u + 5u), d);
            starts[0] = 0;
            starts[1] = n / 3;
            starts[2] = n > 0 ? n - 1 : 0;
            starts[3] = n;
            starts[4] = n > 33 ? n - 33 : 0;
            for (k = 0; k < 5; k++) {
                scans(src, n, starts[k]);
            }
        }
    }
    // Plages homogènes (blancs, lettres) suivies d'un octet unique : chaque octet 0..255, en marche
    // vectorielle (position 37 sur 64 octets) et en marche scalaire (position 5 sur 8 octets).
    for (b = 0; b < 256; b++) {
        memset(src, 'k', 64);
        src[37] = (uint8_t)b;
        scans(src, 64, 0);
        scans(src, 8, 0);
        src[5] = (uint8_t)b;
        scans(src, 8, 0);
        for (k = 0; k < 64; k++) {
            src[k] = (uint8_t)(k % 2 ? ' ' : '\t');
        }
        src[45] = (uint8_t)b;
        scans(src, 64, 1);
        memset(src, 'x', 64);
        src[63] = (uint8_t)b;
        scans(src, 64, 0);
    }
    // Mots-clés : toutes les variantes de casse de chaque mot, puis des octets bruts.
    for (wi = 0; wi < sizeof(words) / sizeof(words[0]); wi++) {
        n = strlen(words[wi]);
        for (m = 0; m < (1u << n); m++) {
            for (k = 0; k < n; k++) {
                w[k] = (uint8_t)words[wi][k];
                if ((m >> k) & 1u && w[k] >= 'a' && w[k] <= 'z') {
                    w[k] = (uint8_t)(w[k] - 32);
                }
            }
            kw(w, n);
        }
    }
    for (n = 0; n <= 6; n++) {
        for (k = 0; k < 64; k++) {
            fill(w, n, (uint32_t)(k * 13u + n), 15);
            kw(w, n);
        }
    }
    puts("ini_scan_oracle: OK");
    return 0;
}
