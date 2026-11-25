/* A fault-injection interposer that shares a name with libc. */
char *fgets(char *buf, int n, void *stream) { return buf; } /* def c.fgets */

int slurp(char *buf, void *stream) {
  /* defined in this file: the call is to it */
  return fgets(buf, 4, stream) != 0; /* call fgets -> c.fgets */
}

void clearerr(void *f) { (void)f; } /* def c.clearerr */
