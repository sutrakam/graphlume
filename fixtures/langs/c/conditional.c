/* Conditional compilation that cuts a declaration apart. */

#ifdef __cplusplus
extern "C" {
#endif

int checked_add(int a,
#ifdef WITH_RANGE
                int b, int limit)
#else
                int b)
#endif
{
    return a + b;
}

static int table[] = {
    1,
#ifdef WITH_TWO
    2,
#endif
    3,
};

int sum_table(void)
{
    return table[0];
}

#ifdef __cplusplus
}
#endif
