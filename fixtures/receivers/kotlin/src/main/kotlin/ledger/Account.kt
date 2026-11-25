package ledger

open class Account(val id: Int) { // def kt.Account
    open fun post(amount: Int) { // def kt.Account.post
    }

    fun audit() { // def kt.Account.audit
    }

    companion object {
        fun open(id: Int): Account { // def kt.Account.open
            return Account(id) // call Account -> new kt.Account
        }
    }
}

data class Entry(val amount: Int) // def kt.Entry

object Registry { // def kt.Registry
    fun lookup(id: Int): Account? { // def kt.Registry.lookup
        return null
    }
}

class Journal { // def kt.Journal
    fun post(amount: Int) { // def kt.Journal.post
    }
}

fun String.shout(): String { // def kt.shout
    return this
}
