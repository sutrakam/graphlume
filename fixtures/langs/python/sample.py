import typing
from typing import Generic, Protocol, TypeVar, overload

T = TypeVar("T")


class ParamType(Generic[T]):
    pass


class StringParamType(ParamType[str]):
    pass


class Pair(pkg.Base[T], typing.Generic[T]):
    pass


class Proto(Protocol[T], mod.Mixin):
    pass


class Plain(Base, metaclass=Meta):
    pass


class P2(Protocol):
    pass


class P3(typing.Protocol):
    pass


class Multi(A[int], B[str, int], c.D[T]):
    pass


@overload
def convert_type(ty: None) -> str: ...
@overload
def convert_type(ty: int) -> int: ...
@typing.overload
def convert_type(ty: str) -> str: ...
def convert_type(ty):
    return ty


class K:
    @overload
    def get(self, a: int) -> int: ...
    @overload
    def get(self, a: str) -> str: ...
    def get(self, a):
        return a

    @property
    def val(self):
        return 1

    @val.setter
    def val(self, v):
        pass


def helper(): pass
def helper(): pass
