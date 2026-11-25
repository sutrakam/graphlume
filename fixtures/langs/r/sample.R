library(stats)

max_items <- 10
greeting = "hello"
20 -> twenty

add <- function(a, b) {
  total <- a + b
  total
}

square = function(x) x * x

scale_all <- \(v, k = 2) v * k

describe <<- function(obj, ...) {
  helper <- function(y) y + 1
  cat(obj, helper(1), "\n")
}

`%+%` <- function(a, b) paste(a, b)

"quoted_fn" <- function() NULL

compute <- function(x,
                    y = 2,
                    ...) {
  result <- add(x, y)
  result
}

Person <- setClass("Person", representation(name = "character", age = "numeric"))

setClass("Account", representation(balance = "numeric"))

setGeneric("deposit", function(object, amount) standardGeneric("deposit"))

setMethod("deposit", "Account", function(object, amount) {
  object@balance <- object@balance + amount
  object
})

setValidity("Account", function(object) TRUE)

Stack <- setRefClass("Stack",
  fields = list(items = "list"),
  methods = list(
    push = function(x) {
      items[[length(items) + 1]] <<- x
    },
    size = function() length(items)
  )
)

print.person <- function(x, ...) {
  cat("Person:", x$name, "\n")
}

make_counter <- function() {
  count <- 0
  function() {
    count <<- count + 1
    count
  }
}

values <- c(1, 2, 3)
doubled <- sapply(values, function(v) v * 2)

if (interactive()) {
  inside_if <- function() 1
}

Queue <- R6Class("Queue",
  public = list(
    items = list(),
    enqueue = function(x) {
      self$items <- c(self$items, x)
      invisible(self)
    }
  ),
  private = list(
    check = function() TRUE
  )
)

setRefClass("Counter", fields = list(n = "numeric"),
  methods = list(
    increment = function() {
      n <<- n + 1
    }
  ))

for (i in 1:3) {
  loop_fn <- function() i
}
