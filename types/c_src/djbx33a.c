#include <stdint.h>

uint64_t djbx33a(const uint8_t *str, int len) {
    uint64_t hash = 5381;
    int i = 0;
    for (; i + 8 <= len; i += 8) {
        hash = ((hash << 5) + hash) + (uint64_t)str[i + 0];
        hash = ((hash << 5) + hash) + (uint64_t)str[i + 1];
        hash = ((hash << 5) + hash) + (uint64_t)str[i + 2];
        hash = ((hash << 5) + hash) + (uint64_t)str[i + 3];
        hash = ((hash << 5) + hash) + (uint64_t)str[i + 4];
        hash = ((hash << 5) + hash) + (uint64_t)str[i + 5];
        hash = ((hash << 5) + hash) + (uint64_t)str[i + 6];
        hash = ((hash << 5) + hash) + (uint64_t)str[i + 7];
    }
    for (; i < len; i++) {
        hash = ((hash << 5) + hash) + (uint64_t)str[i];
    }
    return hash | ((uint64_t)1 << 63);
}
