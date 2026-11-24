/// Move 2024 forms, in the braced module syntax the grammar can read.
module shop::market {
    use std::string::String;

    const EEmpty: u64 = 0;

    public enum Status has copy, drop {
        Open,
        Closed { reason: String },
    }

    public struct Listing has key, store {
        id: UID,
        price: u64,
    }

    public struct Wrapper<T: store>(T) has store;

    public fun open(price: u64, ctx: &mut TxContext): Listing {
        Listing { id: object::new(ctx), price }
    }

    public(package) fun internal_price(l: &Listing): u64 {
        l.price
    }

    entry fun buy(l: Listing) {
        let Listing { id, price: _ } = l;
        id.delete();
    }

    fun status_code(s: &Status): u8 {
        match (s) {
            Status::Open => 0,
            Status::Closed { .. } => 1,
        }
    }
}
