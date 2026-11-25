#define LIB_EXPORT __attribute__((visibility("default")))
#define LIB_INLINE static inline
#define NO_SANITIZE(kind) __attribute__((no_sanitize(kind)))

LIB_INLINE void bump(int *counter)
{
    (*counter)++;
}

static NO_SANITIZE("bounds") int read_at(const int *items, int index)
{
    return items[index];
}

LIB_EXPORT int open_session(void)
{
    return 1;
}

LIB_EXPORT char *session_name(int id)
{
    return 0;
}

int use_session(void)
{
    int n = 0;
    bump(&n);
    return open_session() + read_at(&n, 0);
}
