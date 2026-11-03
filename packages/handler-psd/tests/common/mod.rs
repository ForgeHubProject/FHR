//! Photoshop documents built in code, laid out the way Photoshop saves them:
//! layer records bottom of the stack first with `lyid`/`luni` blocks, groups as
//! a folder record and a closing marker, text as `TySh` with its EngineData,
//! shapes as `SoCo` + `vmsk`, smart objects as `SoLd`, and a raw flattened
//! image after the layers.
#![allow(dead_code)]

/// A document: its canvas and its layers, top of the stack first.
#[derive(Clone)]
pub struct Psd {
    pub width: u32,
    pub height: u32,
    pub layers: Vec<Layer>,
}

#[derive(Clone)]
pub struct Layer {
    pub id: u32,
    pub name: String,
    /// left, top, right, bottom
    pub rect: (i32, i32, i32, i32),
    pub colour: [u8; 4],
    pub opacity: u8,
    pub hidden: bool,
    pub blend: [u8; 4],
    pub blocks: Vec<([u8; 4], Vec<u8>)>,
    /// A group's layers (a group is a folder record above them and a marker below).
    pub children: Option<Vec<Layer>>,
}

impl Layer {
    pub fn pixel(id: u32, name: &str, rect: (i32, i32, i32, i32), colour: [u8; 4]) -> Layer {
        Layer {
            id,
            name: name.into(),
            rect,
            colour,
            opacity: 255,
            hidden: false,
            blend: *b"norm",
            blocks: Vec::new(),
            children: None,
        }
    }

    /// Point text at (x, y): `colour` RGB in 0–1, turned by `angle` degrees.
    pub fn text(
        id: u32,
        content: &str,
        at: (f64, f64),
        font: &str,
        size: f64,
        colour: [f64; 3],
        angle: f64,
    ) -> Layer {
        let (s, c) = angle.to_radians().sin_cos();
        let mut b = Vec::new();
        b.extend(1u16.to_be_bytes());
        for v in [c, s, -s, c, at.0, at.1] {
            b.extend(v.to_be_bytes());
        }
        b.extend(50u16.to_be_bytes());
        b.extend(16u32.to_be_bytes());
        b.extend(descriptor(
            "TxLr",
            vec![
                ("Txt ", Item::Text(content.replace('\n', "\r"))),
                ("EngineData", Item::Data(engine_data(font, size, colour))),
            ],
        ));
        b.extend(1u16.to_be_bytes());
        b.extend(16u32.to_be_bytes());
        b.extend(descriptor(
            "warp",
            vec![("warpStyle", Item::Enum("warpStyle", "warpNone"))],
        ));
        b.extend([0u8; 32]);
        let w = (content.len() as f64 * size * 0.5) as i32;
        let mut l = Layer::pixel(
            id,
            content,
            (
                at.0 as i32,
                (at.1 - size) as i32,
                at.0 as i32 + w,
                at.1 as i32,
            ),
            [0, 0, 0, 255],
        );
        l.blocks.push((*b"TySh", b));
        l
    }

    /// A rectangle shape filled with `rgb`.
    pub fn shape(
        id: u32,
        name: &str,
        rect: (f64, f64, f64, f64),
        canvas: (u32, u32),
        rgb: [f64; 3],
    ) -> Layer {
        let mut l = Layer::pixel(id, name, (0, 0, 0, 0), [0, 0, 0, 0]);
        let mut fill = 16u32.to_be_bytes().to_vec();
        fill.extend(descriptor("null", vec![("Clr ", colour(rgb))]));
        l.blocks.push((*b"SoCo", fill));
        l.blocks.push((*b"vmsk", rect_path(rect, canvas)));
        l
    }

    /// A smart object whose content's corners are `quad` (tl, tr, br, bl).
    pub fn smart(id: u32, name: &str, quad: [f64; 8], source: &str) -> Layer {
        let xs = [quad[0], quad[2], quad[4], quad[6]];
        let ys = [quad[1], quad[3], quad[5], quad[7]];
        let min = |v: [f64; 4]| v.iter().cloned().fold(f64::MAX, f64::min) as i32;
        let max = |v: [f64; 4]| v.iter().cloned().fold(f64::MIN, f64::max) as i32;
        let mut l = Layer::pixel(
            id,
            name,
            (min(xs), min(ys), max(xs), max(ys)),
            [128, 128, 128, 255],
        );
        let mut b = b"soLD".to_vec();
        b.extend(4u32.to_be_bytes());
        b.extend(16u32.to_be_bytes());
        b.extend(descriptor(
            "null",
            vec![
                ("Idnt", Item::Text(source.into())),
                ("placed", Item::Text(source.into())),
                (
                    "Trnf",
                    Item::List(quad.iter().map(|v| Item::Doub(*v)).collect()),
                ),
            ],
        ));
        l.blocks.push((*b"SoLd", b));
        l
    }

    pub fn group(id: u32, name: &str, children: Vec<Layer>) -> Layer {
        let mut l = Layer::pixel(id, name, (0, 0, 0, 0), [0, 0, 0, 0]);
        l.blend = *b"pass";
        l.children = Some(children);
        l
    }

    pub fn opacity(mut self, o: u8) -> Layer {
        self.opacity = o;
        self
    }

    pub fn hidden(mut self) -> Layer {
        self.hidden = true;
        self
    }

    pub fn blend(mut self, key: &[u8; 4]) -> Layer {
        self.blend = *key;
        self
    }

    /// A drop shadow in `rgb`, at `opacity` percent.
    pub fn drop_shadow(mut self, rgb: [f64; 3], opacity: f64) -> Layer {
        let mut b = 0u32.to_be_bytes().to_vec();
        b.extend(16u32.to_be_bytes());
        b.extend(descriptor(
            "null",
            vec![
                ("masterFXSwitch", Item::Bool(true)),
                (
                    "DrSh",
                    Item::Obj(
                        "DrSh",
                        vec![
                            ("enab", Item::Bool(true)),
                            ("Md  ", Item::Enum("BlnM", "Mltp")),
                            ("Clr ", colour(rgb)),
                            ("Opct", Item::Unit("#Prc", opacity)),
                            ("lagl", Item::Unit("#Ang", 120.0)),
                            ("Dstn", Item::Unit("#Pxl", 5.0)),
                            ("blur", Item::Unit("#Pxl", 10.0)),
                        ],
                    ),
                ),
            ],
        ));
        self.blocks.push((*b"lfx2", b));
        self
    }

    pub fn named(mut self, name: &str) -> Layer {
        self.name = name.into();
        self
    }
}

pub enum Item {
    Text(String),
    Doub(f64),
    Long(i32),
    Bool(bool),
    Obj(&'static str, Vec<(&'static str, Item)>),
    List(Vec<Item>),
    Data(Vec<u8>),
    Enum(&'static str, &'static str),
    Unit(&'static str, f64),
}

pub fn colour(rgb: [f64; 3]) -> Item {
    Item::Obj(
        "RGBC",
        vec![
            ("Rd  ", Item::Doub(rgb[0] * 255.0)),
            ("Grn ", Item::Doub(rgb[1] * 255.0)),
            ("Bl  ", Item::Doub(rgb[2] * 255.0)),
        ],
    )
}

fn id(out: &mut Vec<u8>, key: &str) {
    if key.len() == 4 {
        out.extend(0u32.to_be_bytes());
    } else {
        out.extend((key.len() as u32).to_be_bytes());
    }
    out.extend(key.as_bytes());
}

fn unicode(out: &mut Vec<u8>, s: &str) {
    let units: Vec<u16> = s.encode_utf16().chain([0]).collect();
    out.extend((units.len() as u32).to_be_bytes());
    for u in units {
        out.extend(u.to_be_bytes());
    }
}

pub fn descriptor(class: &str, items: Vec<(&str, Item)>) -> Vec<u8> {
    let mut out = Vec::new();
    unicode(&mut out, "");
    id(&mut out, class);
    out.extend((items.len() as u32).to_be_bytes());
    for (key, item) in items {
        id(&mut out, key);
        write_item(&mut out, item);
    }
    out
}

fn write_item(out: &mut Vec<u8>, item: Item) {
    match item {
        Item::Text(s) => {
            out.extend(b"TEXT");
            unicode(out, &s);
        }
        Item::Doub(v) => {
            out.extend(b"doub");
            out.extend(v.to_be_bytes());
        }
        Item::Long(v) => {
            out.extend(b"long");
            out.extend(v.to_be_bytes());
        }
        Item::Bool(v) => {
            out.extend(b"bool");
            out.push(u8::from(v));
        }
        Item::Obj(class, items) => {
            out.extend(b"Objc");
            out.extend(descriptor(class, items));
        }
        Item::List(items) => {
            out.extend(b"VlLs");
            out.extend((items.len() as u32).to_be_bytes());
            for i in items {
                write_item(out, i);
            }
        }
        Item::Data(d) => {
            out.extend(b"tdta");
            out.extend((d.len() as u32).to_be_bytes());
            out.extend(d);
        }
        Item::Enum(ty, v) => {
            out.extend(b"enum");
            id(out, ty);
            id(out, v);
        }
        Item::Unit(unit, v) => {
            out.extend(b"UntF");
            out.extend(unit.as_bytes());
            out.extend(v.to_be_bytes());
        }
    }
}

fn engine_data(font: &str, size: f64, rgb: [f64; 3]) -> Vec<u8> {
    let mut name = vec![0xfe, 0xff];
    for u in font.encode_utf16() {
        name.extend(u.to_be_bytes());
    }
    let mut out = format!(
        "\n\n<<\n\t/EngineDict\n\t<<\n\t\t/StyleRun\n\t\t<<\n\t\t\t/RunArray [\n\t\t\t<<\n\t\t\t\t/StyleSheet\n\t\t\t\t<<\n\t\t\t\t\t/StyleSheetData\n\t\t\t\t\t<<\n\t\t\t\t\t\t/Font 0\n\t\t\t\t\t\t/FontSize {size:.1}\n\t\t\t\t\t\t/AutoLeading true\n\t\t\t\t\t\t/FillColor\n\t\t\t\t\t\t<<\n\t\t\t\t\t\t\t/Type 1\n\t\t\t\t\t\t\t/Values [ 1.0 {:.5} {:.5} {:.5} ]\n\t\t\t\t\t\t>>\n\t\t\t\t\t>>\n\t\t\t\t>>\n\t\t\t>>\n\t\t\t]\n\t\t>>\n\t\t/ParagraphRun\n\t\t<<\n\t\t\t/RunArray [\n\t\t\t<<\n\t\t\t\t/ParagraphSheet\n\t\t\t\t<<\n\t\t\t\t\t/Properties\n\t\t\t\t\t<<\n\t\t\t\t\t\t/Justification 0\n\t\t\t\t\t>>\n\t\t\t\t>>\n\t\t\t>>\n\t\t\t]\n\t\t>>\n\t>>\n\t/ResourceDict\n\t<<\n\t\t/FontSet [\n\t\t<<\n\t\t\t/Name (",
        rgb[0], rgb[1], rgb[2]
    )
    .into_bytes();
    out.extend(name);
    out.extend(b")\n\t\t>>\n\t\t]\n\t>>\n>>");
    out
}

/// A closed rectangle path, in the 8.24 fixed-point canvas fractions a vector
/// mask is saved in.
fn rect_path(r: (f64, f64, f64, f64), (w, h): (u32, u32)) -> Vec<u8> {
    let fixed = |v: f64| ((v * f64::from(1 << 24)) as i32).to_be_bytes();
    let mut b = Vec::new();
    b.extend(3u32.to_be_bytes());
    b.extend(0u32.to_be_bytes());
    let mut record = |kind: u16, body: &[u8]| {
        b.extend(kind.to_be_bytes());
        let mut body = body.to_vec();
        body.resize(24, 0);
        b.extend(body);
    };
    record(6, &[]);
    record(8, &[]);
    record(0, &4u16.to_be_bytes());
    for (x, y) in [(r.0, r.1), (r.2, r.1), (r.2, r.3), (r.0, r.3)] {
        let (fx, fy) = (fixed(x / f64::from(w)), fixed(y / f64::from(h)));
        let mut body = Vec::new();
        for _ in 0..3 {
            body.extend(fy);
            body.extend(fx);
        }
        record(2, &body);
    }
    b
}

impl Psd {
    pub fn new(width: u32, height: u32, layers: Vec<Layer>) -> Psd {
        Psd {
            width,
            height,
            layers,
        }
    }

    /// The `.psd` bytes.
    pub fn save(&self) -> Vec<u8> {
        let mut out = b"8BPS".to_vec();
        out.extend(1u16.to_be_bytes());
        out.extend([0u8; 6]);
        out.extend(3u16.to_be_bytes());
        out.extend(self.height.to_be_bytes());
        out.extend(self.width.to_be_bytes());
        out.extend(8u16.to_be_bytes());
        out.extend(3u16.to_be_bytes());
        out.extend(0u32.to_be_bytes()); // colour mode data
                                        // Resolution: 72 ppi.
        let mut res = b"8BIM".to_vec();
        res.extend(1005u16.to_be_bytes());
        res.extend([0, 0]);
        res.extend(16u32.to_be_bytes());
        res.extend((72u32 << 16).to_be_bytes());
        res.extend([0, 1, 0, 1]);
        res.extend((72u32 << 16).to_be_bytes());
        res.extend([0, 1, 0, 1]);
        out.extend((res.len() as u32).to_be_bytes());
        out.extend(res);

        // Records bottom first: a group is its marker, its layers, then its folder.
        let mut flat: Vec<(Layer, u32)> = Vec::new();
        fn push(l: &Layer, out: &mut Vec<(Layer, u32)>) {
            match &l.children {
                Some(children) => {
                    out.push((Layer::pixel(0, "</Layer group>", (0, 0, 0, 0), [0; 4]), 3));
                    for c in children.iter().rev() {
                        push(c, out);
                    }
                    out.push((l.clone(), 1));
                }
                None => out.push((l.clone(), 0)),
            }
        }
        for l in self.layers.iter().rev() {
            push(l, &mut flat);
        }
        let mut info = (flat.len() as i16).to_be_bytes().to_vec();
        let mut channels = Vec::new();
        for (l, section) in &flat {
            let (left, top, right, bottom) = l.rect;
            let area = ((right - left).max(0) * (bottom - top).max(0)) as usize;
            for v in [top, left, bottom, right] {
                info.extend(v.to_be_bytes());
            }
            info.extend(4u16.to_be_bytes());
            for (ch, value) in [
                (-1i16, l.colour[3]),
                (0, l.colour[0]),
                (1, l.colour[1]),
                (2, l.colour[2]),
            ] {
                info.extend(ch.to_be_bytes());
                info.extend((2 + area as u32).to_be_bytes());
                channels.extend(0u16.to_be_bytes());
                channels.extend(std::iter::repeat_n(value, area));
            }
            info.extend(b"8BIM");
            info.extend(l.blend);
            info.push(l.opacity);
            info.push(0);
            info.push(if l.hidden { 2 } else { 0 });
            info.push(0);
            let mut extra = Vec::new();
            extra.extend(0u32.to_be_bytes());
            extra.extend(0u32.to_be_bytes());
            let name: Vec<u8> = l.name.bytes().take(255).collect();
            extra.push(name.len() as u8);
            extra.extend(&name);
            extra.extend(std::iter::repeat_n(0u8, (4 - (name.len() + 1) % 4) % 4));
            let mut blocks: Vec<([u8; 4], Vec<u8>)> = Vec::new();
            let mut luni = Vec::new();
            unicode(&mut luni, &l.name);
            blocks.push((*b"luni", luni));
            if l.id != 0 {
                blocks.push((*b"lyid", l.id.to_be_bytes().to_vec()));
            }
            if *section != 0 {
                blocks.push((*b"lsct", section.to_be_bytes().to_vec()));
            }
            blocks.extend(l.blocks.iter().cloned());
            for (key, mut data) in blocks {
                if data.len() % 2 == 1 {
                    data.push(0);
                }
                extra.extend(b"8BIM");
                extra.extend(key);
                extra.extend((data.len() as u32).to_be_bytes());
                extra.extend(data);
            }
            info.extend((extra.len() as u32).to_be_bytes());
            info.extend(extra);
        }
        info.extend(channels);
        if info.len() % 2 == 1 {
            info.push(0);
        }
        let mut section = (info.len() as u32).to_be_bytes().to_vec();
        section.extend(info);
        section.extend(0u32.to_be_bytes()); // global layer mask
        out.extend((section.len() as u32).to_be_bytes());
        out.extend(section);

        // The flattened image: white, with each visible pixel layer painted over.
        let (w, h) = (self.width as usize, self.height as usize);
        let mut planes = vec![vec![255u8; w * h]; 3];
        for (l, section) in flat.iter() {
            if *section != 0 || l.hidden || !l.blocks.is_empty() {
                continue;
            }
            let (left, top, right, bottom) = l.rect;
            for y in top.max(0)..bottom.min(h as i32) {
                for x in left.max(0)..right.min(w as i32) {
                    for (plane, value) in planes.iter_mut().zip(l.colour) {
                        plane[y as usize * w + x as usize] = value;
                    }
                }
            }
        }
        out.extend(0u16.to_be_bytes());
        for p in planes {
            out.extend(p);
        }
        out
    }
}
