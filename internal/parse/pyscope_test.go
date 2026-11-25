package parse

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// parsePython parses one Python source under a fixed path.
func parsePython(t *testing.T, src string) *Result {
	t.Helper()
	p := NewRegistry().ForFile("pkg/mod.py")
	if p == nil {
		t.Fatal("no parser for python")
	}
	res, err := p.Parse(context.Background(), "pkg/mod.py", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// receiverOf returns the receiver of the one call written as name, failing the
// test when the source has none or several.
func receiverOf(t *testing.T, res *Result, name string) string {
	t.Helper()
	var found []string
	for _, r := range res.References {
		if r.Name == name && strings.Contains(r.Kind, "call") {
			found = append(found, r.Receiver)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one call to %q, got %d (%v)", name, len(found), found)
	}
	return found[0]
}

// TestPythonDefinitionsAreQualifiedLikeQualname pins the shape ownership is
// read from. Two classes that both declare `save` would yield two symbols
// with ONE qualified name, `pkg/mod.py:save`, which is why `self.save()` could
// only be bound when the file happens to contain a single `save`.
//
// The `<locals>` marker is asserted on purpose. Without it a function's inner
// `helper` would be named `outer.helper`, and the resolver's ownership test —
// "the qualified name ends `<Owner>.<name>`" — would read it as a method of a
// class called `outer`.
func TestPythonDefinitionsAreQualifiedLikeQualname(t *testing.T) {
	res := parsePython(t, `
LIMIT = 3

def top():
    def helper():
        pass
    class Local:
        def run(self):
            pass

class Account:
    def save(self):
        def retry():
            pass

    class Meta:
        def describe(self):
            pass

class Audit:
    @staticmethod
    def save():
        pass
`)
	got := map[string]bool{}
	for _, s := range res.Symbols {
		got[s.Name+" = "+s.FQN] = true
	}
	for _, want := range []string{
		// Module-level definitions keep the default, which the indexing sink
		// fills in as path:name. An explicit one here would be a second
		// spelling of that rule.
		"LIMIT = ",
		"top = ",
		"Account = ",
		"Audit = ",
		"helper = pkg/mod.py:top.<locals>.helper",
		"Local = pkg/mod.py:top.<locals>.Local",
		"run = pkg/mod.py:top.<locals>.Local.run",
		"save = pkg/mod.py:Account.save",
		"retry = pkg/mod.py:Account.save.<locals>.retry",
		"Meta = pkg/mod.py:Account.Meta",
		"describe = pkg/mod.py:Account.Meta.describe",
		"save = pkg/mod.py:Audit.save",
	} {
		if !got[want] {
			t.Errorf("missing %q; symbols: %v", want, keys(got))
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestPythonReceiverIsTheClassTheSourceStates covers every way a receiver's
// class is taken from the source, and — the larger half — every way it is not.
//
// The refusals are the contract. Each `want` that is still the variable's own
// name is a place where the class would have to be INFERRED: from a second
// assignment that may or may not have run, from a function's return value, from
// what a loop iterates over. A wrong class is evidence pointing at the wrong
// method, so those receivers stay exactly what they were before this analysis
// existed.
func TestPythonReceiverIsTheClassTheSourceStates(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"annotated parameter", "def f(acct: Account):\n    acct.close()\n", "Account"},
		{"annotated parameter with a default", "def f(acct: Account = None):\n    acct.close()\n", "Account"},
		{"Optional annotation", "def f(acct: Optional[Account]):\n    acct.close()\n", "Account"},
		{"union with None", "def f(acct: Account | None):\n    acct.close()\n", "Account"},
		{"None first in the union", "def f(acct: None | Account):\n    acct.close()\n", "Account"},
		{"string annotation", "def f(acct: \"Account\"):\n    acct.close()\n", "Account"},
		{"qualified annotation", "def f(acct: models.Account):\n    acct.close()\n", "Account"},
		{"qualified string annotation", "def f(acct: \"models.Account\"):\n    acct.close()\n", "Account"},
		{"constructor assignment", "def f():\n    acct = Account(1)\n    acct.close()\n", "Account"},
		{"qualified constructor", "def f():\n    acct = models.Account(1)\n    acct.close()\n", "Account"},
		{"annotated assignment", "def f():\n    acct: Account = load()\n    acct.close()\n", "Account"},
		{"used in a nested function", "def f():\n    acct = Account(1)\n    def g():\n        acct.close()\n", "Account"},
		{"used in a lambda", "def f():\n    acct = Account(1)\n    return lambda: acct.close()\n", "Account"},
		{"async function", "async def f(acct: Account):\n    await acct.close()\n", "Account"},

		{"untyped parameter", "def f(acct):\n    acct.close()\n", "acct"},
		{"a union of two classes", "def f(acct: Account | Audit):\n    acct.close()\n", "acct"},
		{"a container annotation", "def f(acct: list[Account]):\n    acct.close()\n", "acct"},
		{"a splat is a tuple of the type", "def f(*acct: Account):\n    acct.close()\n", "acct"},
		{"a function's return value", "def f():\n    acct = load()\n    acct.close()\n", "acct"},
		// A class spelled in lower case is indistinguishable from a function
		// call, so the variable is kept.
		{"a lowercase constructor", "def f():\n    acct = account(1)\n    acct.close()\n", "acct"},
		{"a lowercase annotation", "def f(acct: account):\n    acct.close()\n", "acct"},
		// Accepted by design: `name.Capitalised(...)` reads as a class reached
		// through a module, and the resolver still requires a class of exactly
		// that name to declare the method before it believes it.
		{"a capitalised attribute call", "def f(repo):\n    acct = repo.Account(1)\n    acct.close()\n", "Account"},
		{"a lowercase qualified call is a method", "def f(repo):\n    acct = repo.find(1)\n    acct.close()\n", "acct"},
		{"a call on self is a method", "def f(self):\n    acct = self.Account(1)\n    acct.close()\n", "acct"},
		{"assigned twice", "def f(flag):\n    acct = Account(1)\n    if flag:\n        acct = Audit()\n    acct.close()\n", "acct"},
		{"a parameter that is reassigned", "def f(acct: Account):\n    acct = other()\n    acct.close()\n", "acct"},
		{"augmented", "def f():\n    acct = Account(1)\n    acct += 1\n    acct.close()\n", "acct"},
		{"a loop target", "def f(items):\n    acct = Account(1)\n    for acct in items:\n        acct.close()\n", "acct"},
		{"a comprehension target", "def f(items):\n    acct = Account(1)\n    return [acct.close() for acct in items]\n", "acct"},
		{"a with target", "def f():\n    acct = Account(1)\n    with open_it() as acct:\n        acct.close()\n", "acct"},
		{"an except target", "def f():\n    acct = Account(1)\n    try:\n        pass\n    except E as acct:\n        acct.close()\n", "acct"},
		{"unpacked", "def f():\n    acct = Account(1)\n    acct, other = pair()\n    acct.close()\n", "acct"},
		{"a walrus", "def f():\n    acct = Account(1)\n    if (acct := find()):\n        acct.close()\n", "acct"},
		{"declared global", "def f():\n    global acct\n    acct = Account(1)\n    acct.close()\n", "acct"},
		{"shadowed by a nested parameter", "def f():\n    acct = Account(1)\n    def g(acct):\n        acct.close()\n", "acct"},
		{"shadowed by a lambda parameter", "def f():\n    acct = Account(1)\n    return lambda acct: acct.close()\n", "acct"},
		{"a nested function of the same name", "def f():\n    acct = Account(1)\n    def acct():\n        pass\n    acct.close()\n", "acct"},
		{"a local import of the same name", "def f():\n    acct = Account(1)\n    import acct\n    acct.close()\n", "acct"},
		// A module-level variable's class is never read, even when it is
		// assigned once: its name is an indexed symbol, and the compiler-import
		// path tells a receiver from a callee by that name.
		{"a module-level variable", "acct = Account(1)\ndef f():\n    acct.close()\n", "acct"},
		{"a class attribute is not a local", "class C:\n    acct = Account(1)\n    def f(self):\n        acct.close()\n", "acct"},

		{"self is left for the resolver", "class C:\n    def f(self):\n        self.close()\n", "self"},
		{"cls is left for the resolver", "class C:\n    @classmethod\n    def f(cls):\n        cls.close()\n", "cls"},
		{"the class itself", "def f():\n    Account.close()\n", "Account"},
		{"super()", "class C(B):\n    def f(self):\n        super().close()\n", "super"},
		{"super with arguments", "class C(B):\n    def f(self):\n        super(C, self).close()\n", "super"},

		{"self attribute initialized in init", "class C:\n    def __init__(self, repo: OrderRepo):\n        self.repo = repo\n    def f(self):\n        self.repo.close()\n", "OrderRepo"},
		{"self attribute constructed in init", "class C:\n    def __init__(self):\n        self.repo = OrderRepo()\n    def f(self):\n        self.repo.close()\n", "OrderRepo"},
		{"self attribute annotated in init", "class C:\n    def __init__(self):\n        self.repo: OrderRepo = None\n    def f(self):\n        self.repo.close()\n", "OrderRepo"},
		{"class-level attribute annotation", "class C:\n    repo: OrderRepo\n    def f(self):\n        self.repo.close()\n", "OrderRepo"},

		{"an attribute chain", "class C:\n    def f(self):\n        self.repo.close()\n", OpaqueReceiver},
		{"a call result", "def f():\n    make().close()\n", OpaqueReceiver},
		{"a subscript", "def f(rows):\n    rows[0].close()\n", OpaqueReceiver},
		{"a string literal", "def f():\n    \"x\".close()\n", OpaqueReceiver},
		{"a bare call has no receiver", "def f():\n    close()\n", ""},
		// The method name sits two lines below where the expression begins. The
		// line-keyed receiver map lost this; the receiver must survive.
		{"a call spread over lines", "def f(acct: Account):\n    result = (\n        acct\n        .close()\n    )\n", "Account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := receiverOf(t, parsePython(t, tc.src), "close"); got != tc.want {
				t.Errorf("receiver = %q, want %q\n%s", got, tc.want, tc.src)
			}
		})
	}
}

// TestPythonChainedCallsOfOneNameAreOpaque covers the one shape a position
// cannot disambiguate: `a.f().f()` is two calls to `f` beginning at the same
// character. Giving both the inner call's receiver would claim the outer one
// was made on `a`, so neither keeps a name.
func TestPythonChainedCallsOfOneNameAreOpaque(t *testing.T) {
	res := parsePython(t, "def run(acct: Account):\n    acct.copy().copy()\n")
	n := 0
	for _, r := range res.References {
		if r.Name != "copy" {
			continue
		}
		n++
		if r.Receiver != OpaqueReceiver {
			t.Errorf("receiver = %q, want %q", r.Receiver, OpaqueReceiver)
		}
	}
	if n != 2 {
		t.Fatalf("want two calls to copy, got %d", n)
	}
}

// TestPythonObjectOfACallIsNotACall covers the reference the tags query
// invents: the OBJECT of `row.save()` is tagged as a call to `row`.
//
// Where `row` is a local value that reference is dropped — it is not a call,
// and it bound to whatever function the repository happened to name `row`.
// Where it is not local it is kept, as it always was. And `run.run()`, where
// the variable and the method share a name and so two references arrive at one
// position under one name, must lose exactly one of them: dropping both lost
// the call itself, which is how six real call sites left a measurement.
func TestPythonObjectOfACallIsNotACall(t *testing.T) {
	count := func(res *Result, name string) (n int, receivers []string) {
		for _, r := range res.References {
			if r.Name == name {
				n++
				receivers = append(receivers, r.Receiver)
			}
		}
		return n, receivers
	}

	res := parsePython(t, "def f(row):\n    row.save()\n")
	if n, _ := count(res, "row"); n != 0 {
		t.Errorf("a parameter used as a receiver is recorded as %d call(s) to itself", n)
	}

	res = parsePython(t, "import logging\nlogger = logging.getLogger()\ndef f():\n    logger.warning('x')\n")
	// A module-level name in receiver position is not kept as a "call" to
	// that name, even though it is a use of something the index
	// holds. It is not a call, and binding it would produce an edge from the
	// function to the VARIABLE — 30% of flask's call edges without the scope
	// walk, and every `bp.route(...)` otherwise.
	if n, _ := count(res, "logger"); n != 0 {
		t.Errorf("a module-level name used as a receiver is recorded as %d call(s) to itself", n)
	}

	res = parsePython(t, "def f():\n    run = CrawlerRun()\n    run.run()\n")
	n, receivers := count(res, "run")
	if n != 1 || receivers[0] != "CrawlerRun" {
		t.Errorf("run.run(): %d reference(s) named run with receivers %v, want one, on CrawlerRun",
			n, receivers)
	}
}

// TestPythonCallThroughALocalValueIsMarked: `callback()` where callback is a
// parameter is a call site whose target the source does not state.
func TestPythonCallThroughALocalValueIsMarked(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		indirect  bool
	}{
		{"a parameter", "def f(callback):\n    callback()\n", true},
		{"an assigned variable", "def f():\n    callback = pick()\n    callback()\n", true},
		{"a loop variable", "def f(cbs):\n    for callback in cbs:\n        callback()\n", true},
		{"a lambda parameter", "def f():\n    return lambda callback: callback()\n", true},
		{"a name from the enclosing function", "def f(callback):\n    def g():\n        callback()\n", true},
		{"a nested definition is a name, not a value", "def f():\n    def callback():\n        pass\n    callback()\n", false},
		{"a function-local import is a name, not a value", "def f():\n    from m import callback\n    callback()\n", false},
		{"a module-level name", "def f():\n    callback()\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := parsePython(t, tc.src)
			found := false
			for _, r := range res.References {
				if r.Name != "callback" || !strings.Contains(r.Kind, "call") {
					continue
				}
				found = true
				if got := strings.HasSuffix(r.Kind, IndirectCallSuffix); got != tc.indirect {
					t.Errorf("kind %q: indirect = %v, want %v\n%s", r.Kind, got, tc.indirect, tc.src)
				}
			}
			if !found {
				t.Fatalf("no call to callback recorded\n%s", tc.src)
			}
		})
	}
}

// TestPythonImportedMembersAreRecorded pins the row that says which NAME an
// import brought in, and the rows that must not exist.
func TestPythonImportedMembersAreRecorded(t *testing.T) {
	res := parsePython(t, `
import os
import numpy as np
from shop.models import Account, Ledger
from shop.utils.fmt import money as cash
from .helpers import Helper
from . import sibling
from shop.star import *

def late():
    from shop.late import Lazy
`)
	got := map[string]bool{}
	for _, imp := range res.Imports {
		got[imp.Path+" "+imp.Alias] = true
	}
	for _, want := range []string{
		"os ",
		"numpy np",
		"shop.models ",
		"shop.models .Account",
		"shop.models .Ledger",
		"shop.utils.fmt ",
		// A relative import keeps its dots, on the module row and the member
		// row alike, so the resolver can anchor it to the importing file.
		".helpers ",
		".helpers .Helper",
		// A bare relative import names the package itself.
		". .sibling",
		"shop.star ",
		// A function-local import is recorded for the file: a bare use of the
		// name anywhere else in it would be an error unless a definition in
		// the file bound it, and that is consulted first.
		"shop.late .Lazy",
	} {
		if !got[want] {
			t.Errorf("missing import %q; got %v", want, keys(got))
		}
	}
	for _, unwanted := range []string{
		// A renamed member shares no name with the definition, so there is
		// nothing for a reference written `cash` to be weighed against.
		"shop.utils.fmt .money", "shop.utils.fmt .cash", "shop.utils.fmt cash",
		// The shape the import query must not record: a member's name as a
		// module path, under the member's local name as a module alias.
		"money cash",
		// The dotless spellings the import query must not record.
		"helpers ", "helpers .Helper", "sibling ", "sibling .sibling",
	} {
		if got[unwanted] {
			t.Errorf("recorded %q, which must not be", unwanted)
		}
	}
}
