// A macro invocation is not a call of the function of the same name. fd bound
// std's `format!` to a test function named `format`.
fn format(width: usize) -> usize { // def rs.format
    width
}

fn describe(width: usize) -> String { // def rs.describe
    let rows = vec![width, width]; // call vec -> external
    println!("{}", rows.len()); // call println -> external
    format!("{}", width) // call format -> external
}

// A macro of the repository is bound by its invocation, in its file and from
// another one, and a call of a function of the same name is not that macro.
macro_rules! print_error { // def rs.print_error_macro
    ($($arg:tt)*) => {{
        eprintln!($($arg)*);
    }};
}

fn print_error(msg: &str) { // def rs.print_error
    let _ = msg;
}

fn report(msg: &str) { // def rs.report
    print_error!("{}", msg); // call print_error -> rs.print_error_macro
    print_error(msg); // call print_error -> rs.print_error
}
