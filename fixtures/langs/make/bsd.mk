# BSD make directives start with a dot; the grammar reads some as assignments.
PROG= hello
.if defined(DEBUG)
CFLAGS+= -g
.endif
.include <bsd.prog.mk>
.include "local.mk"
