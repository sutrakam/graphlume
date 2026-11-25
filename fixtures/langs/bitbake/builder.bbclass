# Fixture class with shell functions the grammar reads as errors. The last
# one is recovered from a variable_assignment whose text is `name() {`.
BUILDER_TARGET ?= "all"
PREFERRED_PROVIDER_virtual/builder ?= "builder-default"

builder_run() {
	bbnote ${DESTDIR:+DESTDIR=${DESTDIR} }${VERBOSE} builder "$@" -- ${EXTRA_ARGS}
	eval ${DESTDIR:+DESTDIR=${DESTDIR} }${VERBOSE} builder "$@" -- ${EXTRA_ARGS}
}

builder_do_compile()  {
	builder_run --target ${BUILDER_TARGET}
}

builder_do_install() {
	DESTDIR='${D}' builder_run --target install
}

EXPORT_FUNCTIONS do_compile do_install
