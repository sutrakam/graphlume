#include <stddef.h>

struct pending;
struct wire;

typedef struct wire wire_t;
typedef unsigned long counter_t;

struct point {
    int x;
    int y;
};

typedef struct {
    size_t used;
    size_t cap;
} buffer_t;

typedef struct entry {
    const char *key;
    struct entry *next;
} entry_t;

typedef int (*compare_fn)(const void *, const void *);
typedef entry_t *(*lookup_fn)(const char *key, size_t len);

static counter_t total;
static const int limit = 64;

int add(int a, int b)
{
    return a + b;
}

char *dup_name(const char *src)
{
    return (char *)src;
}

static const char **name_table(void)
{
    return NULL;
}

int run(void)
{
    struct point p = {1, 2};
    return add(p.x, p.y);
}
