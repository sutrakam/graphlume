use std::fmt;

static GLOBAL: u32 = 1;
static mut COUNTER: u32 = 0;
const LIMIT: usize = 3;
pub type Alias = Vec<u8>;

mod inline {
    pub fn helper() {}
    pub mod deep {
        pub fn deeper() {}
    }
}

mod external;

macro_rules! print_error {
    ($($arg:tt)*) => {{ eprintln!($($arg)*); }};
}

trait Shape {
    type Output;
    const SIDES: u32;
    fn area(&self) -> f64;
}

struct Sq(f64);

impl Shape for Sq {
    type Output = f64;
    const SIDES: u32 = 4;
    fn area(&self) -> f64 { self.0 * self.0 }
}

impl Iterator for Sq {
    type Item = u8;
    fn next(&mut self) -> Option<u8> { None }
}

fn main() {
    let v = vec![1, 2, 3];
    println!("{}", v.len());
    let s = format!("{}", 1);
    print_error!("bad {}", s);
    std::println!("x");
    foo(1);
    assert_eq!(1, 1);
    let m = HashMap::new();
    thread_local! { static TL: u32 = 1; }
}
