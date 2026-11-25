package com.acme.query

class Evaluator extends Base {

    Map handlers = [
        (Op.Equals): { Op.Equals eq, Prop prop, Closure fn = null, boolean onValue = false ->
            handlers[Op.Equals].call(new Op.Equals(eq.name, null), prop, fn)
        }
    ]

    protected def resolve(Op pc, boolean unique = true) {
        def value = pc.value
        return value
    }

    protected lookup(name, obj) {
        if (name.contains('.')) {
            obj
        }
        else {
            name
        }
    }

    int total() { 1 }

    protected static populate(items, out) {
        out.putAll(items)
    }
}
