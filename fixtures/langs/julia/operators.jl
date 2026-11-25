struct Weights
    len::Int
end

Base.:(==)(a::Weights, b::Weights) = a.len == b.len
Base.:(<)(a::Weights, b::Weights)   = a.len < b.len
Base.:+(a::Weights, b::Weights) = Weights(a.len + b.len)
Base.isequal(a::Weights, b::Weights) = a.len == b.len

Base.:(==)(a::Weights, b::Int) where {T} = false

function Base.:(>=)(a::Weights, b::Weights)
    a.len >= b.len
end

function Base.:(!=)(a::Weights, b::Weights)
    !(a == b)
end

function helper(w::Weights)
    cmp(a, b) = a.len < b.len
    cmp(w, w)
end
