//! Reading big-endian fields out of a byte slice, every read bounds-checked:
//! a damaged or hostile file is an error, never a panic.

pub(crate) type Result<T> = std::result::Result<T, String>;

#[derive(Clone)]
pub(crate) struct Reader<'a> {
    bytes: &'a [u8],
    at: usize,
}

impl<'a> Reader<'a> {
    pub(crate) fn new(bytes: &'a [u8]) -> Self {
        Reader { bytes, at: 0 }
    }

    pub(crate) fn pos(&self) -> usize {
        self.at
    }

    pub(crate) fn left(&self) -> usize {
        self.bytes.len() - self.at
    }

    pub(crate) fn take(&mut self, n: usize) -> Result<&'a [u8]> {
        let end = self
            .at
            .checked_add(n)
            .filter(|e| *e <= self.bytes.len())
            .ok_or_else(|| format!("the file ends early (wanted {n} bytes at {})", self.at))?;
        let out = &self.bytes[self.at..end];
        self.at = end;
        Ok(out)
    }

    pub(crate) fn skip(&mut self, n: usize) -> Result<()> {
        self.take(n).map(|_| ())
    }

    /// The next `n` bytes as a reader of their own.
    pub(crate) fn sub(&mut self, n: usize) -> Result<Reader<'a>> {
        Ok(Reader::new(self.take(n)?))
    }

    fn array<const N: usize>(&mut self) -> Result<[u8; N]> {
        let mut out = [0; N];
        out.copy_from_slice(self.take(N)?);
        Ok(out)
    }

    pub(crate) fn u8(&mut self) -> Result<u8> {
        Ok(self.array::<1>()?[0])
    }
    pub(crate) fn u16(&mut self) -> Result<u16> {
        Ok(u16::from_be_bytes(self.array()?))
    }
    pub(crate) fn i16(&mut self) -> Result<i16> {
        Ok(i16::from_be_bytes(self.array()?))
    }
    pub(crate) fn u32(&mut self) -> Result<u32> {
        Ok(u32::from_be_bytes(self.array()?))
    }
    pub(crate) fn i32(&mut self) -> Result<i32> {
        Ok(i32::from_be_bytes(self.array()?))
    }
    pub(crate) fn u64(&mut self) -> Result<u64> {
        Ok(u64::from_be_bytes(self.array()?))
    }
    pub(crate) fn i64(&mut self) -> Result<i64> {
        Ok(i64::from_be_bytes(self.array()?))
    }
    pub(crate) fn f64(&mut self) -> Result<f64> {
        Ok(f64::from_be_bytes(self.array()?))
    }
    pub(crate) fn key(&mut self) -> Result<[u8; 4]> {
        self.array()
    }

    /// A length field: 4 bytes, or 8 in a large document (`.psb`) where `wide`.
    pub(crate) fn len(&mut self, wide: bool) -> Result<usize> {
        let n = if wide {
            self.u64()?
        } else {
            u64::from(self.u32()?)
        };
        usize::try_from(n).map_err(|_| "a length past this machine's memory".to_string())
    }

    /// A UTF-16 string with its length in characters before it.
    pub(crate) fn unicode(&mut self) -> Result<String> {
        let n = self.u32()? as usize;
        let raw = self.take(n.checked_mul(2).ok_or("a string longer than memory")?)?;
        let units: Vec<u16> = raw
            .chunks_exact(2)
            .map(|c| u16::from_be_bytes([c[0], c[1]]))
            .collect();
        Ok(String::from_utf16_lossy(&units)
            .trim_end_matches('\0')
            .to_string())
    }

    /// A Pascal string: one length byte, the bytes (Mac Roman; read as Latin-1).
    pub(crate) fn pascal(&mut self) -> Result<String> {
        let n = self.u8()? as usize;
        Ok(self.take(n)?.iter().map(|b| char::from(*b)).collect())
    }
}

/// FNV-1a: a stable 64-bit fingerprint of pixels and blocks, to tell whether
/// they changed without keeping them.
pub(crate) fn fingerprint(parts: &[&[u8]]) -> u64 {
    let mut h: u64 = 0xcbf2_9ce4_8422_2325;
    for part in parts {
        for b in part.iter() {
            h ^= u64::from(*b);
            h = h.wrapping_mul(0x0100_0000_01b3);
        }
        h ^= 0xff;
        h = h.wrapping_mul(0x0100_0000_01b3);
    }
    h
}
