pub trait Named { // def rs.Named
    fn name(&self) -> String; // def rs.Named.name
}

pub trait Shape: Named + Clone + Debug { // super Named -> rs.Named; nosuper Clone; nosuper Debug
    fn area(&self) -> f64; // def rs.Shape.area

    fn describe(&self) -> String { // def rs.Shape.describe
        self.name() // call name -> rs.Named.name
    }
}

// Shares the method name with Named and is not a supertrait of Shape.
pub struct Plain;

impl Plain {
    pub fn name(&self) -> String { // def rs.Plain.name
        String::new()
    }
}

// A struct that shares its name with a standard trait. It is not what a trait
// bound `Debug` means.
pub struct Debug; // def rs.Debug

// `impl Trait for Type` declares that Type implements Trait. The edge starts at
// the type, which is not the symbol on the impl's line.
pub struct Circle; // def rs.Circle

impl Named for Circle { // super Named -> rs.Named
    fn name(&self) -> String { // def rs.Circle.name
        String::new()
    }
}

pub struct Wrapper<T>(T); // def rs.Wrapper

impl<T> Named for Wrapper<T> { // super Named -> rs.Named
    fn name(&self) -> String { // def rs.Wrapper.name
        String::new()
    }
}

// A path-qualified trait: the last segment names it.
impl shapes::Named for Plain { // super Named -> rs.Named
    fn name(&self) -> String { // def rs.Plain.named
        String::new()
    }
}

// A blanket impl has no single type, so it records nothing.
impl<T: Named> Shape for T { // nosuper Shape
    fn area(&self) -> f64 { // def rs.Blanket.area
        0.0
    }
}

// The standard library's trait, not the struct above that shares its name.
impl Debug for Circle { // nosuper Debug
}
