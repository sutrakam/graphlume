package com.acme.flat

/** The class itself has a one-line doc comment. */
class Flat implements Marker {
    protected final Map<String, Item> items = new LinkedHashMap<String, Item>()
    /** a one-line doc comment: the grammar never closes it */
    int size

    /***/
    Flat(int size) {
        this.size = size
    }

    /** */
    void emptied() {}

    /**/
    void plain() {}

    /** same line */ void sameLine() {}

    /**
     * A doc comment over several lines is read properly.
     */
    void spread() {}

    String quoted() {
        return "/** not a comment */"
    }

    /** closing */
    int count() { items.size() }

    static Flat of(int size) { new Flat(size) }
}

class Tail {
    /** last one */
    void finish() {}

    def helper(String s) { s.trim() }
}
