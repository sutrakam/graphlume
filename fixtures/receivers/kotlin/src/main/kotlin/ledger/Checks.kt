package ledger

// `require(x)` is kotlin's. One class declares a private `require` of its own,
// and a call in that class is to it; a call anywhere else is not.
class Reader {
    private fun <T> require(token: Int): T { // def kt.Reader.require
        throw IllegalStateException()
    }

    fun peek(): String {
        return require<String>(1) // call require -> kt.Reader.require high
    }
}

class Validator {
    fun check(x: Int) {
        require(x > 0) // call require -> none
    }
}
