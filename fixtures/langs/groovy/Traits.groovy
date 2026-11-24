package com.acme.stock

import groovy.transform.CompileStatic

/**
 * A reactive entity.
 */
@CompileStatic
trait Repo<D> implements Base<D>, Auditable, Serializable {

    @Override
    boolean validate(Map args) {
        true
    }

    static D create() {
        (D) this.newInstance()
    }

    static <T> T withConnection(String name, Closure<T> callable) {
        callable.call()
    }

    String describe() { 'repo' }
}

trait Plain<T> {
    void run() {}

    static T make() {
        null
    }
}

trait Wide<T> extends Base {
    void widen() {}
}

trait Simple implements Base, Auditable {
    void simple() {}
}

class Holder {
    int size

    Holder() {
        size = 0
    }

    // not a trait: a call inside a method is not a definition
    void use() {
        trait(1)
        println size
    }
}
