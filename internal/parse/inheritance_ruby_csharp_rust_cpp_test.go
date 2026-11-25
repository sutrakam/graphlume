package parse

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Supertype references for Ruby, C#, Rust and C++. The shared contract (the
// capture names reach the right edge kind, the query is not dropped by the
// grammar) is pinned in inheritance_test.go; these cases pin what each query
// captures and, as important, what it must not.

func TestInheritanceReferencesRubyCSharpRustCpp(t *testing.T) {
	cases := []struct {
		name string
		file string
		src  string
		want []string
	}{
		{
			// A superclass is EXTENDS, qualified or not; a mixin is IMPLEMENTS,
			// whichever of the three keywords brings it in, and a list of
			// modules is one reference each.
			name: "ruby_superclass_and_mixins",
			file: "zoo.rb",
			src: `class Dog < Animal
  include Walkable
  extend Forwardable, Util::Sizing
  prepend Loud
end
class Puppy < Zoo::Dog
end
module Pack
  include Walkable
end`,
			want: []string{
				"EXTENDS:Animal", "IMPLEMENTS:Walkable", "IMPLEMENTS:Forwardable", "IMPLEMENTS:Sizing",
				"IMPLEMENTS:Loud", "EXTENDS:Dog", "IMPLEMENTS:Walkable",
			},
		},
		{
			// A struct can only implement interfaces and an interface can only
			// extend them; a class list is split by the I-prefix convention; a
			// record's base call is a class; an enum's list is its integral type.
			name: "csharp_base_lists",
			file: "Zoo.cs",
			src: `namespace Zoo {
public class Hound : Creature, IWalker, IList<int>, Ns.IFoo, Ns.Base<T> {}
public struct Pose : IWalker {}
public interface IRunner : IWalker, Other.IThing {}
public record Pup(int Age) : Hound(Age), IDisposable;
public class Box<T> : Container<T> where T : IFoo {}
public enum Level : byte { Low }
}`,
			want: []string{
				"EXTENDS:Creature", "IMPLEMENTS:IWalker", "IMPLEMENTS:IList", "IMPLEMENTS:IFoo", "EXTENDS:Base",
				"IMPLEMENTS:IWalker",
				"EXTENDS:IWalker", "EXTENDS:IThing",
				"EXTENDS:Hound", "IMPLEMENTS:IDisposable",
				"EXTENDS:Container",
			},
		},
		{
			// Supertraits are EXTENDS. Lifetimes and where clauses are not
			// supertraits, and a standard-library trait is not captured at all:
			// the resolver binds by name and would find a same-named struct.
			name: "rust_supertraits",
			file: "shapes.rs",
			src: `pub trait Shape: Named + Clone {}
pub trait Gen<T>: ns::Base<T> + 'static {}
pub trait Other: io::Read {}
pub trait Free where Self: Sized {}
pub trait Flag: Debug + Send + Sync + 'static {}
pub trait Mine: Named + std::fmt::Display + io::Read {}`,
			want: []string{"EXTENDS:Named", "EXTENDS:Base", "EXTENDS:Named"},
		},
		{
			// `impl Trait for Type` is IMPLEMENTS from Type to Trait, read by
			// the trait's last segment, with the type in any of its four
			// spellings. Inherent impls, blanket impls, impls for a type that
			// names no type (a reference, a slice) and the standard library's
			// traits give nothing.
			name: "rust_impl_trait_for_type",
			file: "impls.rs",
			src: `impl Sink for S {}
impl<T> Sink for W<T> {}
impl Sink for path::P {}
impl a::b::Sink for Q {}
impl<T> a::Sink<T> for path::R<T> where T: Bound {}
impl S {}
impl<T: Bound> Sink for T {}
impl<'a, T> Sink for &'a mut T {}
impl Sink for [u8] {}
impl Debug for S {}
impl fmt::Display for S {}`,
			want: []string{
				"IMPLEMENTS:Sink", "IMPLEMENTS:Sink", "IMPLEMENTS:Sink", "IMPLEMENTS:Sink", "IMPLEMENTS:Sink",
			},
		},
		{
			// Every base is EXTENDS, with or without an access specifier or
			// virtual; qualified and template bases are read by their last
			// segment.
			name: "cpp_base_clauses",
			file: "zoo.cc",
			src: `class A : public B, private C, D {};
struct S : B {};
template <typename T> class Tm : public Base<T>, public ns::Other<T, int> {};
class Q : public ns::inner::B {};
class V : virtual public ::Top {};`,
			want: []string{
				"EXTENDS:B", "EXTENDS:C", "EXTENDS:D",
				"EXTENDS:B",
				"EXTENDS:Base", "EXTENDS:Other",
				"EXTENDS:B",
				"EXTENDS:Top",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := supertypeRefs(parse(t, tc.file, tc.src))
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("supertype references mismatch\n got: %v\nwant: %v", got, want)
			}
		})
	}
}

// A file that declares no hierarchy yields no supertype reference, and the
// shapes that merely look like one are not read as one.
func TestNoSupertypeReferenceForRubyCSharpRustCpp(t *testing.T) {
	cases := []struct{ name, file, src string }{
		{"ruby_plain", "plain.rb", `module Shop
  class Plain
    include? 3
    def run(x); x.include(Mod); helper(Other); end
  end
end
class Built < Struct.new(:a)
end
class Dyn < Class.new(Base)
end`},
		{"ruby_nonconstant_mixin", "mix.rb", `class A
  include some_helper
  extend self
end`},
		{"csharp_plain", "Plain.cs", `class Plain {
  void Run() { this.Go(); Other.Use(); }
}
enum Level : byte { Low }
interface IBare { void Go(); }`},
		{"rust_plain", "plain.rs", `pub trait Free where Self: Sized { fn go(&self); }
pub struct S;
impl S { fn go(&self) {} }
#[derive(Debug, Clone)]
pub struct W;`},
		{"cpp_plain", "plain.cc", `class Plain { public: void f() { g(); } };
struct S { int x; };
class Dep : public Base<T>::type {};
class Fwd;`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := supertypeRefs(parse(t, tc.file, tc.src)); len(got) != 0 {
				t.Errorf("file declares no inheritance but produced %v", got)
			}
		})
	}
}

// TestRustImplCarriesItsType: the reference for `impl Trait for Type` records
// Type, by its last segment, as the receiver with the implementer shape; a
// supertrait reference records none. The resolver starts the edge there.
func TestRustImplCarriesItsType(t *testing.T) {
	res := parse(t, "impls.rs", `pub trait Shape: Named {}
impl Sink for S {}
impl<T> a::Sink<T> for path::R<T> {}
impl<T: Bound> Sink for T {}`)
	var got []string
	for _, r := range res.References {
		got = append(got, fmt.Sprintf("%s|%s|%s|%s", r.Kind, r.Name, r.Receiver, r.ReceiverKind))
	}
	want := []string{
		"reference.extends|Named||",
		"reference.implementation|Sink|S|implementer",
		"reference.implementation|Sink|R|implementer",
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("references\n got: %q\nwant: %q", got, want)
	}
}

// One clause must yield one reference per type, whichever way it is spelled.
func TestSupertypeReferencesAreNotDuplicatedRubyCSharpRustCpp(t *testing.T) {
	cases := []struct {
		file, src string
		want      int
	}{
		{"d.rb", "class A < Zoo::B\n  include Util::M\nend", 2},
		{"D.cs", "class A : Ns.B<T>, Ns.IFoo, C {}", 3},
		{"d.rs", "trait A: ns::B<T> + C {}", 2},
		{"d.cc", "class A : public ns::B<T>, public ns::inner::C, D {};", 3},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			if got := supertypeRefs(parse(t, tc.file, tc.src)); len(got) != tc.want {
				t.Errorf("want %d references, got %v", tc.want, got)
			}
		})
	}
}

// A C# call through `this` or `base` is a call. Both are keyword tokens in the
// grammar, so the identifier-on-identifier pattern never matched them, and a
// method inherited from a base class was unreachable through either.
func TestCSharpThisAndBaseCallsAreExtracted(t *testing.T) {
	res := parse(t, "A.cs", `class A : B {
  void M() { this.X(); base.Y(); Z(); f.W(); }
}`)
	var got []string
	for _, r := range res.References {
		if r.Kind == "reference.call" {
			got = append(got, r.Name+"/"+r.Receiver)
		}
	}
	sort.Strings(got)
	want := "W/f X/this Y/base Z/"
	if strings.Join(got, " ") != want {
		t.Errorf("calls = %q, want %q", strings.Join(got, " "), want)
	}
}
