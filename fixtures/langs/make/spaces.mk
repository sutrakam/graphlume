# A non-GNU makefile whose recipes are indented with spaces. The grammar reads
# some recipe lines as rules; names that cannot be targets are not captured.
SRC_X = extend\ftxgasp.c extend\ftxkern.c &
        extend\ftxcmap.c extend\ftxwidth.c

all: .symbolic
  @-erase $(OBJS_M)
  @-erase *.err
