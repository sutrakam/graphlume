// Fixture Move sources. Declarations at module level are definitions;
// struct fields, locals, calls and use declarations are not.
module 0x42::ledger {
    use std::vector;
    use std::signer;

    const E_NOT_FOUND: u64 = 1;
    const MAX_ENTRIES: u64 = 1000;

    struct Entry has copy, drop, store {
        owner: address,
        amount: u64,
    }

    struct Ledger<phantom T> has key {
        entries: vector<Entry>,
        count: u64,
    }

    struct Marker has drop {}

    public struct Receipt has key, store {
        id: u64,
    }

    fun helper(x: u64): u64 {
        let doubled = x * 2;
        doubled + MAX_ENTRIES
    }

    public fun create_entry(owner: address, amount: u64): Entry {
        Entry { owner, amount }
    }

    public(friend) fun friend_only(ledger: &mut Ledger<u64>) {
        ledger.count = ledger.count + 1;
    }

    public entry fun record<T>(account: &signer, amount: u64) acquires Ledger {
        let addr = signer::address_of(account);
        let ledger = borrow_global_mut<Ledger<T>>(addr);
        vector::push_back(&mut ledger.entries, create_entry(addr, amount));
        ledger.count = helper(ledger.count);
    }

    entry fun init_entry(account: &signer) {
        move_to(account, Ledger<u64> { entries: vector::empty(), count: 0 });
    }

    native fun hash_bytes(data: vector<u8>): vector<u8>;

    public native fun native_public(x: u64): u64;

    #[view]
    public fun total(addr: address): u64 acquires Ledger {
        borrow_global<Ledger<u64>>(addr).count
    }

    #[test]
    fun test_helper() {
        assert!(helper(1) == 1002, E_NOT_FOUND);
    }

    spec helper {
        ensures result == x * 2 + MAX_ENTRIES;
    }

    spec fun spec_double(x: u64): u64 {
        x * 2
    }
}

module 0x42::registry {
    use 0x42::ledger::{Self, Entry};

    const VERSION: u8 = 3;

    struct Registry has key {
        items: vector<Entry>,
    }

    public fun version(): u8 {
        VERSION
    }

    public fun add(reg: &mut Registry, owner: address) {
        let e = ledger::create_entry(owner, 1);
        vector::push_back(&mut reg.items, e);
    }
}

script {
    fun main(account: &signer) {
        ledger::init_entry(account);
    }
}

address 0x2 {
    module Legacy {
        struct Cell has copy, drop { value: u64 }

        public fun make(value: u64): Cell {
            Cell { value }
        }
    }

    module Plain {
        const LIMIT: u64 = 7;
    }
}
