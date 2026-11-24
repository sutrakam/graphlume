use regex::RegexBuilder;

mod messages;

pub fn messages() {} // def lib.messages

pub struct Wrapper {
    inner: regex::Regex,
}

impl Wrapper {
    pub fn is_match(&self, text: &str) -> bool { // def Wrapper.is_match
        self.inner.is_match(text) // call is_match -> none
    }

    pub fn check(&self) -> bool { // def Wrapper.check
        if messages::errored() { // call errored -> messages.errored high; nocall messages
            return false;
        }
        self.is_match("x") // call is_match -> Wrapper.is_match high
    }
}

pub struct SearcherBuilder;

impl SearcherBuilder {
    pub fn new() -> SearcherBuilder { // def SearcherBuilder.new
        SearcherBuilder
    }

    pub fn line_number(self, yes: bool) -> SearcherBuilder { // def SearcherBuilder.line_number
        self
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn line_number() { // def tests.line_number
        let _ = RegexBuilder::new("a").multi_line(true); // call new -> none
        let _ = SearcherBuilder::new().line_number(false); // call line_number -> SearcherBuilder.line_number
    }
}
