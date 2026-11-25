# Substitution references the grammar reads: every definition around them is kept.
SRC = a.c b.c
OBJ = $(SRC:.c=.o)
PAT = $(SRC:%.c=build/%.o)
REN = $(SRC:a=b)
DIRS = $(dir $(SRC:/=x))
LAST = 1
all: $(OBJ)
	echo $(PAT)
