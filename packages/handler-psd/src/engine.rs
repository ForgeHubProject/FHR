//! A text layer's EngineData: the PostScript-like dictionary Photoshop keeps
//! its text styling in —
//! `<< /EngineDict << /StyleRun << /RunArray [ << /StyleSheet << … >> >> ] >> >> >>`.
//! Read into JSON: dictionaries are objects (keys without their `/`), names
//! are strings with theirs (`"/Name"`), and strings are decoded from the
//! UTF-16 Photoshop writes them in.

use serde_json::{Map, Number, Value};

use crate::bytes::Result;

const MAX_DEPTH: usize = 64;

pub(crate) fn parse(bytes: &[u8]) -> Result<Value> {
    let mut p = Parser { b: bytes, i: 0 };
    p.value(0)
        .map_err(|e| format!("text styling (EngineData): {e}"))
}

struct Parser<'a> {
    b: &'a [u8],
    i: usize,
}

fn delimiter(c: u8) -> bool {
    c.is_ascii_whitespace() || matches!(c, b'/' | b'[' | b']' | b'<' | b'>' | b'(' | b')' | 0)
}

impl Parser<'_> {
    fn peek(&self) -> Option<u8> {
        self.b.get(self.i).copied()
    }

    fn ws(&mut self) {
        while self
            .peek()
            .is_some_and(|c| c.is_ascii_whitespace() || c == 0)
        {
            self.i += 1;
        }
    }

    fn starts(&self, s: &[u8]) -> bool {
        self.b[self.i..].starts_with(s)
    }

    fn value(&mut self, depth: usize) -> Result<Value> {
        if depth > MAX_DEPTH {
            return Err("nested too deep".into());
        }
        self.ws();
        match self.peek() {
            Some(b'<') if self.starts(b"<<") => self.dict(depth),
            Some(b'[') => self.array(depth),
            Some(b'(') => self.string(),
            Some(b'/') => {
                self.i += 1;
                Ok(Value::String(format!("/{}", self.word())))
            }
            Some(_) => {
                let word = self.word();
                match word.as_str() {
                    "true" => Ok(Value::Bool(true)),
                    "false" => Ok(Value::Bool(false)),
                    w => w
                        .parse::<f64>()
                        .ok()
                        .and_then(Number::from_f64)
                        .map(Value::Number)
                        .ok_or_else(|| format!("unexpected {w:?} at {}", self.i)),
                }
            }
            None => Err("ends early".into()),
        }
    }

    fn word(&mut self) -> String {
        let start = self.i;
        while self.peek().is_some_and(|c| !delimiter(c)) {
            self.i += 1;
        }
        String::from_utf8_lossy(&self.b[start..self.i]).into_owned()
    }

    fn dict(&mut self, depth: usize) -> Result<Value> {
        self.i += 2;
        let mut map = Map::new();
        loop {
            self.ws();
            if self.starts(b">>") {
                self.i += 2;
                return Ok(Value::Object(map));
            }
            if self.peek() != Some(b'/') {
                return Err(format!("expected a key at {}", self.i));
            }
            self.i += 1;
            let key = self.word();
            let value = self.value(depth + 1)?;
            map.insert(key, value);
        }
    }

    fn array(&mut self, depth: usize) -> Result<Value> {
        self.i += 1;
        let mut out = Vec::new();
        loop {
            self.ws();
            match self.peek() {
                Some(b']') => {
                    self.i += 1;
                    return Ok(Value::Array(out));
                }
                None => return Err("an array without its end".into()),
                _ => out.push(self.value(depth + 1)?),
            }
        }
    }

    /// `( … )` with `\` escapes; UTF-16 (after a byte-order mark) or Latin-1.
    fn string(&mut self) -> Result<Value> {
        self.i += 1;
        let mut raw = Vec::new();
        loop {
            match self.peek() {
                None => return Err("a string without its end".into()),
                Some(b')') => {
                    self.i += 1;
                    break;
                }
                Some(b'\\') => {
                    let c = self.b.get(self.i + 1).copied().ok_or("ends early")?;
                    raw.push(match c {
                        b'n' => b'\n',
                        b'r' => b'\r',
                        b't' => b'\t',
                        other => other,
                    });
                    self.i += 2;
                }
                Some(c) => {
                    raw.push(c);
                    self.i += 1;
                }
            }
        }
        let text = if raw.starts_with(&[0xfe, 0xff]) {
            let units: Vec<u16> = raw[2..]
                .chunks_exact(2)
                .map(|c| u16::from_be_bytes([c[0], c[1]]))
                .collect();
            String::from_utf16_lossy(&units)
        } else {
            raw.iter().map(|b| char::from(*b)).collect()
        };
        Ok(Value::String(text))
    }
}
