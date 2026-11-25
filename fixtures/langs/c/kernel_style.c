/* C as an operating system kernel writes it: macros that stand for
 * annotations, declarations and whole functions. Nothing here is copied from
 * another project. */

#define BIT_OF(n) (1UL << (n))
#define clamp_to(v, lo, hi) \
	((v) < (lo) ? (lo) : (v) > (hi) ? (hi) : (v))
#define PLAIN_LIMIT 64

#define scan_table(key)					\
static int scan_##key(const char *text, int *out)	\
{							\
	int i;						\
	for (i = 0; i < 4; i++) {			\
		if (text[i] == (key))			\
			return 0; /* found */		\
	}						\
	return 1; /* not found */			\
}

DEFINE_LOCK(table_lock);
static DEFINE_LIST(devices);
static unsigned long counter __initdata = 3;
static int table[4] __read_mostly;

struct packed_hdr {
	int kind;
	void __iomem *regs;
} __packed;

static int __init start_up(void)
{
	return 0;
}

static void __iomem *map_regs(unsigned long base)
{
	return (void *)base;
}

asmlinkage void __noreturn halt_now(int code)
{
	for (;;)
		;
}

static int scale(int v) __must_hold(table_lock)
{
	return v * 2;
}

SYSCALL_DEFINE2(poke, int, fd, unsigned long, val)
{
	return fd + val;
}

COMPAT_SYSCALL_DEFINE1(peek, int, fd)
{
	return fd;
}

static BPF_CALL_2(helper_add, u64, a, u64, b)
{
	return a + b;
}

TEST_F(widget, resize)
{
	int x = 1;
}

static int walk(struct list_head *head)
{
	struct item *it = container_of(head, struct item, link);
	void *p = kzalloc_obj(struct item);

	list_for_each_entry(it, head, link) {
		int seen = 1;
		use(it, seen);
	}
	asm_inline volatile("nop" ::: "memory");
	pr_info(KERN_INFO "walked %d\n", counter);
	return 0;
}

int last_function(void)
{
	return 1;
}

void __printf(2, 3)
__log_line(const char *fmt, ...)
{
}

int link_ok(struct cmd *cmd)
{
	return ethtool_has(cmd, 10000baseT_Full);
}

EXPORT_SYMBOL(last_function);
