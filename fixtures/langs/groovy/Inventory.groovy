package com.acme.stock

import groovy.transform.CompileStatic

@CompileStatic
class Inventory extends BaseStore implements Serializable, Comparable<Inventory> {
    static final String DEFAULT_NAME = 'main'
    public static int created = 0
    private final Map<String, Integer> counts = [:]
    String name
    def cache = [:]
    List<String> tags
    Closure<String> formatter = { String s -> s.toUpperCase() }
    def handler = { x -> x * 2 }

    Inventory() {
        this('default')
    }

    Inventory(String name) {
        this.name = name
    }

    private Inventory(String name, int size) { this(name) }

    def add(String sku, int qty = 1) {
        counts[sku] = (counts[sku] ?: 0) + qty
        return this
    }

    static Inventory create(String name) {
        new Inventory(name)
    }

    private <T> T intercept(T instance) {
        instance
    }

    public List<String> skus() { counts.keySet().toList() }

    protected void reset() {
        counts.clear()
    }

    int compareTo(Inventory other) { name <=> other.name }

    String toString() { "Inventory($name)" }

    def "maximum of two numbers"() {
        expect:
        Math.max(1, 3) == 3
    }

    abstract static class Nested {
        abstract void run()
    }

    static class Entry implements Serializable {
        String sku
        int qty
        Entry(String sku, int qty) { this.sku = sku; this.qty = qty }
        boolean isEmpty() { qty == 0 }
    }
}

interface Repository<T> {
    T find(String id)
    List<T> findAll()
    void save(T item)
    default String describe() { 'repo' }
}

trait Auditable {
    String auditLog = ''
    void audit(String msg) { auditLog += msg }
    abstract String owner()
}

enum Status implements Serializable {
    ACTIVE('A'), INACTIVE('I')

    final String code

    Status(String code) { this.code = code }

    String label() { name().toLowerCase() }
}

@interface Marker {
    String value() default ''
}

class Account extends Exception {
    Account(String msg) { super(msg) }
}

def helper(String x) {
    println x
}

String shout(String text) { text.toUpperCase() }

static void util() { }

def threshold = 10
final LIMIT = 5
String banner = "welcome"

def run = { String s -> println s }

helper('hi')
new Inventory('x').add('a')
