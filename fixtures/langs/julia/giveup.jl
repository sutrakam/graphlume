# The ternary below, with an indexed parenthesised backslash expression as its
# else branch, makes the grammar give up on the whole file: the root is an ERROR
# and the items before the break are its children. What follows the break is read
# as the rows of a matrix: the short form `shift` is lost; the long forms, the
# struct and the const are still found.

row_scale(x::Int) = 2x
column_scale(x::Int, y::Int) = x * y

function solve!(r, a, b, l, i, j)
    r[i,j] = l == 0 ? 1 : (a \ (b))[end]
    r
end

shift(x::Int) = x + 1

function after(x)
    local_double(y) = 2y
    local_double(x)
end

struct Pair2
    first::Int
    second::Int
end

const LIMIT = 10
