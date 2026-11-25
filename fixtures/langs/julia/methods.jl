module Methods

function plain(x)
    x + 1
end

function typed(x)::Int
    1
end

function Base.get(d::Dict, k, default)
    default
end

function Base.:+(a::Vec, b::Vec)
    Vec(a.x + b.x)
end

function bounded(x::T) where {T<:Number}
    x
end

function both(x::T)::T where T
    x
end

function Vec{T}(x) where {T}
    Vec(x)
end

function nomethods end

function (f::Callable)(x)
    f.fn(x)
end

short(x) = x * 2
short2(x::Int)::Int = x
Base.isempty(s::Vec) = s.n == 0
Base.:-(a::Vec) = Vec(-a.x)
*(a::Vec, b::Real) = Vec(a.x * b)
Vec{T}(a, b) where {T} = Vec(a)
bound(x::T) where T = x
(f::Callable)(x, y) = f.fn(x, y)

@inline fast(x) = x
@noinline function slow(x)
    x
end

macro twice(ex)
    quote
        $ex
        $ex
    end
end

macro short(ex) esc(ex) end

if VERSION >= v"1.9"
    versioned(x) = x
else
    function versioned(x)
        x + 0
    end
end

function outer(n)
    helper(x) = x + n
    function nested(y)
        y
    end
    f = x -> x
    map(helper, 1:n)
end

result = short(3)
value = plain(1) + typed(2)

end
