package parse

import (
	"sort"
	"strings"
	"testing"
)

func refRows(res *Result) []string {
	var out []string
	for _, r := range res.References {
		out = append(out, r.Kind+" "+r.Name+" recv="+r.Receiver+"/"+r.ReceiverKind+" args="+arityText(r.ArgCount))
	}
	sort.Strings(out)
	return out
}

func TestCSharpNewIsAConstruction(t *testing.T) {
	res := parse(t, "a.cs", `class A
{
    void M()
    {
        var a = new Widget(3);
        var b = new Shop.Widget();
        var c = new Box<int>(1, 2);
        var d = new Shop.Box<int>();
        Widget e = new();
        var f = new { X = 1 };
        var g = new int[3];
        Foo(h);
    }
}
`)
	var got []string
	for _, r := range refRows(res) {
		if strings.HasPrefix(r, "reference.call.new") {
			got = append(got, r)
		}
	}
	wantList(t, "constructions", got, []string{
		"reference.call.new Box recv=/bare args=2",
		"reference.call.new Box recv=Shop/name args=0",
		"reference.call.new Widget recv=/bare args=1",
		"reference.call.new Widget recv=Shop/name args=0",
	})
}

func TestCSharpConstructorDelegation(t *testing.T) {
	res := parse(t, "a.cs", `class Derived : Base
{
    public Derived() : this(1, 2) { }
    public Derived(int a, int b) : base(a) { }
    public Derived(string s) { }
}
class Loner
{
    public Loner() : base() { }
    public Loner(int a) : this() { }
}
`)
	var got []string
	for _, r := range refRows(res) {
		if strings.HasPrefix(r, "reference.call ") {
			got = append(got, r)
		}
	}
	wantList(t, "delegations", got, []string{
		"reference.call Base recv=super/super args=1",
		"reference.call Derived recv=this/self args=2",
		"reference.call Loner recv=this/self args=0",
	})
}

// Return types and bodies the grammar could not read were methods.
func TestCSharpReturnTypeIsNotAMethod(t *testing.T) {
	res := parse(t, "a.cs", `#if FEATURE_DEFAULT_INTERFACE
using System.Diagnostics;
#endif

namespace Serilog;

public interface ILogger
{
    ILogger ForContext(IEnumerable<ILogEventEnricher> enrichers)
#if FEATURE_DEFAULT_INTERFACE
    {
        if (enrichers == null!)
            return this;
        return ForContext(new SafeAggregateEnricher(enrichers));
    }
#else
        ;
#endif
}

public class Logger
{
    public ILogger ForContext(string name) => this;
    public Task<Result> Load(int id) => null;
}
`)
	for _, s := range res.Symbols {
		switch s.Name {
		case "ILogger", "Task", "Result":
			if string(s.Kind) == "method" {
				t.Errorf("a return type was recorded as the method %s at line %d", s.Name, s.Range.Start.Line)
			}
		}
	}
	var names []string
	for _, s := range res.Symbols {
		if s.Range.Start.Line >= 21 {
			names = append(names, s.Name)
		}
	}
	wantList(t, "Logger members", names, []string{"Logger", "ForContext", "Load"})
}
