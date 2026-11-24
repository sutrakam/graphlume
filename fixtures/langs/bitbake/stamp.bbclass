# Fixture class file.
STAMP_DIR ?= "${TMPDIR}/stamps"
STAMP_FORMAT ??= "%Y%m%d"
STAMP_LIST = ""
STAMP_LIST += "base"
STAMP_LIST:append = " extra"
STAMP_PREFIX .= "-stamp"
STAMP_SUFFIX:class-target = ".tgt"
export STAMP_HOME = "${HOME}/.stamps"
VIRTUAL-RUNTIME_stamper ?= "stamper-default"

inherit_defer ${STAMP_BASE}
inherit logging

EXPORT_FUNCTIONS do_stamp

def stamp_name(d, kind):
    """Build the stamp file name."""
    fmt = d.getVar('STAMP_FORMAT')
    return "%s-%s" % (kind, fmt)

def _private_helper(d):
    return d.getVar('STAMP_DIR')

python __anonymous() {
    if d.getVar('STAMP_FORMAT') is None:
        d.setVar('STAMP_FORMAT', '%Y')
}

python stamp_handler() {
    bb.note("stamp event %s" % e.data)
}
addhandler stamp_handler
stamp_handler[eventmask] = "bb.event.BuildStarted"

stamp_do_stamp() {
    touch ${STAMP_DIR}/${PN}.stamp
}

stamp_cleanup:prepend() {
    rm -f ${STAMP_DIR}/${PN}.stamp
}

python do_stamp_report() {
    names = [stamp_name(d, k) for k in ['a', 'b']]
    bb.plain(" ".join(names))
}

do_stamp[dirs] = "${STAMP_DIR}"
do_stamp[doc] = "write a stamp"
addtask stamp_report after do_stamp
addtask do_stamp before do_build
EXPORT_FUNCTIONS stamp_do_stamp
BBCLASSEXTEND = "native nativesdk"
