#include <unistd.h>

void flush(int fd) { /* def c.flush */
    write(fd, "x", 1); /* call write -> none */
    MACRO_CALL(fd); /* call MACRO_CALL -> none */
}
