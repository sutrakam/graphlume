# Fixture recipe. Top-level assignments, shell and Python tasks, functions and
# addtask lines are definitions; inherit, include, require and flag settings are not.
SUMMARY = "Demo application"
DESCRIPTION = "A recipe exercising each declaration form."
LICENSE = "MIT"
LIC_FILES_CHKSUM = "file://COPYING;md5=0123456789abcdef0123456789abcdef"

inherit cmake pkgconfig
require demo-common.inc
include conf/extra.inc

PV = "1.2.3"
SRC_URI = "git://example.com/demo.git;protocol=https;branch=main"
SRCREV = "0123456789abcdef0123456789abcdef01234567"
S = "${WORKDIR}/git"

DEPENDS += "zlib openssl"
DEPENDS:append = " libpng"
RDEPENDS:${PN} ?= "bash"
PACKAGECONFIG ??= "gl"
PACKAGECONFIG[gl] = "-DENABLE_GL=ON,-DENABLE_GL=OFF,virtual/libgl"
EXTRA_OECMAKE:append:qemux86 = " -DQEMU=1"
export CFLAGS_EXTRA = "-O2"
FILES:${PN} += "${bindir}/demo"
D_VAR := "${@d.getVar('PV')}"

do_configure:prepend() {
    echo "preparing ${PN}"
}

do_compile() {
    oe_runmake
}

do_install() {
    install -d ${D}${bindir}
    install -m 0755 demo ${D}${bindir}
}

do_install:append:class-native() {
    echo done
}

helper_function() {
    echo "helper"
}

fakeroot do_fix_perms() {
    chmod 0755 ${D}${bindir}/demo
}

python do_check() {
    import os
    if not os.path.exists(d.getVar('S')):
        bb.fatal("no sources")
}

python do_stamp_version() {
    d.setVar('STAMP', d.getVar('PV'))
}

python helper_python() {
    bb.note("python helper")
}

fakeroot python do_rootfs_hook() {
    bb.debug(1, "hook")
}

python () {
    d.setVar('ANON', '1')
}

def compute_flags(d, extra):
    flags = d.getVar('CFLAGS') or ''
    return flags + extra

def other_helper(d):
    return d.getVar('PN')

addtask check after do_compile before do_install
addtask stamp_version before do_build
addtask fix_perms after do_install
do_check[depends] = "zlib:do_populate_sysroot"
do_compile[dirs] = "${B}"
EXPORT_FUNCTIONS do_configure do_compile
