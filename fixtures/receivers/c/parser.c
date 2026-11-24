struct callbacks {
    void (*error)(void *ctx);
};

struct parser {
    struct callbacks callbacks;
};

void report(void *ctx);

void run(struct parser *parser, void *p_ctx) {
    parser->callbacks.error(p_ctx); /* call error -> none */
    report(p_ctx); /* call report -> c.report */
}
