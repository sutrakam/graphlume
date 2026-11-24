package parse

import "strings"

// Ruby. The class and module patterns and the calls are in the supplement
// (queries.go); this registers the methods, replacing the inferred query.
//
// The inferred query is `(method (identifier) @name)`, which takes the first
// identifier child of a method, so a method whose name is not an identifier had
// no symbol, and an endless method `def +(o) = o` was named after its body.
// Seven kinds of name exist in a def, and each is written as the source writes
// it:
//
//	def run                   run
//	def run?  def run!        run?  run!
//	def x=(v)                 x=       a setter (the grammar's `setter` node)
//	def [](k)  def ==(o)      []  ==   an operator
//	def GET                   GET      a constant: Rack::Builder's `def GET`
//	def self.build            build    owned by the class, as before
//	def obj.helper            helper
//
// # Methods that are not written with def
//
//	attr_reader :a, :b        field a, field b
//	attr_writer :a            field a
//	attr_accessor :a          field a
//	alias new old             method new
//	alias :new :old           method new
//	alias_method :new, :old   method new
//	define_method(:name) {}   method name
//	define_method("name") {}  method name
//
// An attribute is a field, not a pair of methods: a reader and a writer of one
// name are one declaration, a call `obj.a` is a use of it, and listing `a` and
// `a=` would double what the source states once. A name that is not a literal
// symbol (`attr_reader name`, `define_method(name)`) is computed at run time and
// defines nothing here.
//
// # Calls
//
// `include M`, `extend M` and `prepend M` as a bare statement are not calls.
// They are recorded as supertype references to M (queries.go), and recording a
// call of a method named include as well bound 18 of them to an unrelated
// `include` in rack. With a receiver (`foo.include(M)`) they stay calls.
//
// `super` and `super(...)` are calls, recorded under the name of the method
// they are written in, on the receiver `super` (model.ReceiverSuper), which is
// the shape the resolver starts at the class's ancestors from. See
// delegation.go.
func init() {
	registerDefinitions("ruby", `
(class (constant) @name) @definition.class
(module (constant) @name) @definition.module
(assignment left: (constant) @name) @definition.constant

(method . (identifier) @name) @definition.method
(method . (constant) @name) @definition.method
(method . (setter) @name) @definition.method
(method . (operator) @name) @definition.method
(singleton_method name: (identifier) @name) @definition.method
(singleton_method name: (constant) @name) @definition.method
(singleton_method name: (setter) @name) @definition.method
(singleton_method name: (operator) @name) @definition.method

(call !receiver method: (identifier) @_m arguments: (argument_list (simple_symbol) @name)
  (#any-of? @_m "attr_reader" "attr_writer" "attr_accessor")) @definition.field
(alias . (identifier) @name) @definition.method
(alias . (simple_symbol) @name) @definition.method
(alias . (setter) @name) @definition.method
(alias . (operator) @name) @definition.method
(alias . (constant) @name) @definition.method
(call !receiver method: (identifier) @_m arguments: (argument_list . (simple_symbol) @name)
  (#eq? @_m "alias_method")) @definition.method
(call !receiver method: (identifier) @_m arguments: (argument_list . (simple_symbol) @name)
  (#eq? @_m "define_method")) @definition.method
(call !receiver method: (identifier) @_m arguments: (argument_list . (string (string_content) @name))
  (#eq? @_m "define_method")) @definition.method
`, true)
	registerNameFix("ruby", func(name string) string {
		return strings.TrimPrefix(name, ":")
	})
}
