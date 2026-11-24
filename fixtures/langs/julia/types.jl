"""
Geometry types: modules, structs, abstract and primitive types, constants.
"""
module Geometry

using LinearAlgebra
import Base: show, +

const ORIGIN = (0.0, 0.0)
const Pair2{T} = Tuple{T, T}
const Numeric = Union{Int, Float64}
const global SCALE = 2.0
const LIMIT::Int = 100
const LO, HI = 1, 9

counter = 0
global total = 10

abstract type Shape end
abstract type Solid{T<:Real} <: Shape end
primitive type Octet 8 end

struct Point{T<:Real} <: Shape
    x::T
    y::T
    Point(x::T, y::T) where {T<:Real} = new{T}(x, y)
    function Point(x)
        new{typeof(x)}(x, x)
    end
end

mutable struct Cursor
    pos::Point{Int}
    history::Vector{Point{Int}}
end

struct Plain
    a
end

@enum Color red green blue
@enum Level::UInt8 begin
    low = 1
    high
end

Base.@kwdef struct Options
    depth::Int = 3
end

module Inner
    const DEPTH = 2
end

baremodule Bare
end

end # module
