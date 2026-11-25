package ledger

import ledger.Registry
import ledger.Entry as Row
import java.util.ArrayList

class Savings(id: Int) : Account(id) { // def kt.Savings
    override fun post(amount: Int) { // def kt.Savings.post
        super.post(amount) // call post -> kt.Account.post high
        audit() // call audit -> kt.Account.audit high
        this.audit() // call audit -> kt.Account.audit high
        val a = Account(1) // call Account -> new kt.Account
        val e = Entry(amount) // call Entry -> new kt.Entry
        val r = Row(amount) // call Row -> none
        Account.open(2) // call open -> kt.Account.open high
        Registry.lookup(3) // call lookup -> kt.Registry.lookup
        "x".shout() // call shout -> kt.shout high
        println("x") // call println -> none
        listOf(1).map { it } // call listOf -> none; call map -> none
        val xs = ArrayList<String>() // call ArrayList -> external
        xs.add("q") // call add -> none
        xs?.clear() // call clear -> none
    }

    fun transfer(from: Account, journal: Journal, other: Any) { // def kt.Savings.transfer
        from.audit() // call audit -> kt.Account.audit
        journal.post(1) // call post -> kt.Journal.post
        other.post(1) // call post -> none
        from?.post(2) // call post -> kt.Account.post high
    }
}
