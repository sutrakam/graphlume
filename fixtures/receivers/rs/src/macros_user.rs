// The crate-wide use of a macro_rules! defined in macros.rs.
fn elsewhere(msg: &str) { // def rs.elsewhere
    print_error!("{}", msg); // call print_error -> rs.print_error_macro
}
