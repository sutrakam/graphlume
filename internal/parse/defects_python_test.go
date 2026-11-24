package parse

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestPythonSubscriptedBases(t *testing.T) {
	res := parse(t, "a.py", `import typing
class ParamType(Generic[T]): pass
class StringParamType(ParamType[str]): pass
class Pair(pkg.Base[T], typing.Generic[T]): pass
class Proto(Protocol[T], mod.Mixin): pass
class P2(Protocol): pass
class P3(typing.Protocol): pass
class Multi(A[int], B[str, int], c.D[T], metaclass=Meta): pass
class Plain(Base): pass
`)
	var got []string
	for _, r := range res.References {
		if strings.HasPrefix(r.Kind, "reference.inherit") {
			got = append(got, r.Name)
		}
	}
	sort.Strings(got)
	wantList(t, "bases", got, []string{
		"A", "B", "Base", "Base", "D", "Mixin", "ParamType",
	})
}

func TestPythonOverloadStubsFold(t *testing.T) {
	res := parse(t, "a.py", `from typing import overload
import typing

@overload
def convert_type(ty: None) -> str: ...
@overload
def convert_type(ty: int) -> int: ...
@typing.overload
def convert_type(ty: str) -> str: ...
def convert_type(ty):
    return ty

def other(): pass

class K:
    @overload
    def get(self, a: int) -> int: ...
    @overload
    def get(self, a: str) -> str: ...
    def get(self, a):
        return a

    @property
    def val(self): return 1

    @val.setter
    def val(self, v): pass

    @staticmethod
    def make(): pass
    @staticmethod
    def make(): pass
`)
	var got []string
	for _, s := range res.Symbols {
		got = append(got, s.Name+" "+strconv.Itoa(s.Range.Start.Line)+"-"+strconv.Itoa(s.Range.End.Line))
	}
	wantList(t, "symbols", got, []string{
		"convert_type 5-11", "other 13-13", "K 15-32", "get 17-21", "val 24-24", "val 27-27", "make 30-30", "make 32-32",
	})
}
