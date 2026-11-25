/* libc's fgets, in a file that neither defines nor declares one. */
int readline_from(char *buf, void *stream) {
  return fgets(buf, 4, stream) != 0; /* call fgets -> none */
}

/* The rest of the stream functions are libc's too. */
void reset_stream(void *stream) {
  clearerr(stream); /* call clearerr -> none */
}
