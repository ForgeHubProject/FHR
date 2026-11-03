//! A Photoshop document (`.psd`, and `.psb`, its large-document form), read
//! from Adobe's published file format: the header, the image resources the
//! diff reports, every layer with the blocks that say what kind of layer it
//! is, and where the flattened image Photoshop saves after the layers starts.
//!
//! Layers keep the id Photoshop gives them (`lyid`) across saves, so a
//! renamed, moved or restyled layer is the same layer on both sides. Pixels
//! aren't decoded for the diff: each layer's channels are fingerprinted as
//! saved, which tells an edit from none.

use std::collections::BTreeMap;

use serde_json::Value;

use crate::bytes::{fingerprint, Reader, Result};
use crate::descriptor;

/// One Photoshop document.
pub(crate) struct Document {
    /// A large document (`.psb`): some lengths are 8 bytes there.
    pub wide: bool,
    pub width: u32,
    pub height: u32,
    /// Bits per channel: 1, 8, 16 or 32.
    pub depth: u16,
    /// Colour mode: 0 bitmap, 1 grayscale, 2 indexed, 3 RGB, 4 CMYK, 7
    /// multichannel, 8 duotone, 9 Lab.
    pub mode: u16,
    pub channels: u16,
    /// Pixels per inch, from the resolution resource.
    pub resolution: Option<f64>,
    /// Guides: `"x 120"`, `"y 48.5"` (pixels from the left or top).
    pub guides: Vec<String>,
    /// Every layer, top of the stack first; a group's layers follow it.
    pub layers: Vec<Layer>,
    /// The flattened image's first channel holds its transparency.
    pub merged_alpha: bool,
    /// Where the flattened image starts in the file.
    pub composite_at: Option<usize>,
    /// Smart objects' embedded files, by their unique id: fingerprints.
    pub embedded: BTreeMap<String, u64>,
    /// An indexed image's palette: 256 reds, then greens, then blues.
    pub palette: Vec<u8>,
}

impl Document {
    /// The side of a diff where the file doesn't exist.
    pub(crate) fn empty() -> Document {
        Document {
            wide: false,
            width: 0,
            height: 0,
            depth: 8,
            mode: 3,
            channels: 0,
            resolution: None,
            guides: Vec::new(),
            layers: Vec::new(),
            merged_alpha: false,
            composite_at: None,
            embedded: BTreeMap::new(),
            palette: Vec::new(),
        }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub(crate) struct Rect {
    pub left: f64,
    pub top: f64,
    pub right: f64,
    pub bottom: f64,
}

impl Rect {
    pub fn width(&self) -> f64 {
        self.right - self.left
    }
    pub fn height(&self) -> f64 {
        self.bottom - self.top
    }
    pub fn is_empty(&self) -> bool {
        self.width() <= 0.0 || self.height() <= 0.0
    }
    pub fn union(self, o: Rect) -> Rect {
        if self.is_empty() {
            return o;
        }
        if o.is_empty() {
            return self;
        }
        Rect {
            left: self.left.min(o.left),
            top: self.top.min(o.top),
            right: self.right.max(o.right),
            bottom: self.bottom.max(o.bottom),
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub(crate) enum Kind {
    Pixel,
    Text,
    /// A vector shape: a fill layer drawn through a vector mask.
    Shape,
    /// A solid colour, gradient or pattern fill over the whole canvas.
    Fill,
    Smart,
    Group,
    /// An adjustment layer, by Photoshop's name for it ("Curves").
    Adjustment(&'static str),
}

pub(crate) struct Layer {
    /// Photoshop's layer id, or one made from the name where a file has none.
    pub id: u32,
    pub name: String,
    pub kind: Kind,
    /// The group the layer is in.
    pub parent: Option<u32>,
    /// Its pixels' bounds on the canvas (empty for a layer without pixels).
    pub rect: Rect,
    pub visible: bool,
    pub opacity: u8,
    /// Fill opacity: the layer's own pixels without its effects.
    pub fill_opacity: u8,
    pub blend: [u8; 4],
    pub clipped: bool,
    /// Lock flags: transparency, pixels, position, all.
    pub locks: u32,
    /// Colour label (0 none, 1 red … 7 grey).
    pub label: u16,
    /// Fingerprint of the colour and transparency channels.
    pub pixels: u64,
    pub mask: Option<Mask>,
    pub vector: Option<Vector>,
    pub text: Option<Text>,
    pub placed: Option<Placed>,
    /// A shape's or fill layer's content: the solid colour, gradient or
    /// pattern descriptor.
    pub fill: Option<Value>,
    /// A shape's stroke descriptor.
    pub stroke: Option<Value>,
    /// Layer effects.
    pub effects: Option<Value>,
    /// An adjustment layer's settings, fingerprinted.
    pub settings: Option<u64>,
}

pub(crate) struct Mask {
    pub disabled: bool,
    pub pixels: u64,
}

pub(crate) struct Vector {
    /// The path's bounds on the canvas.
    pub rect: Rect,
    pub path: u64,
    pub disabled: bool,
}

pub(crate) struct Text {
    pub content: String,
    /// `xx xy yx yy tx ty`: where the text sits and how it's turned and scaled.
    pub transform: [f64; 6],
    /// Each styled run, in order (font, size, colour…), with defaults filled in.
    pub runs: Vec<Style>,
    pub align: Option<&'static str>,
    pub warp: Option<String>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct Style {
    pub font: Option<String>,
    pub size: Option<f64>,
    pub colour: Option<[f64; 4]>,
    pub tracking: Option<f64>,
    pub leading: Option<f64>,
    pub bold: bool,
    pub italic: bool,
    pub underline: bool,
}

pub(crate) struct Placed {
    /// The placed content's corners on the canvas: top-left, top-right,
    /// bottom-right, bottom-left (x, y each).
    pub quad: [f64; 8],
    /// The embedded or linked file it shows.
    pub source: Option<String>,
}

/// Additional-layer-info keys whose length is 8 bytes in a large document.
const WIDE_KEYS: &[&[u8; 4]] = &[
    b"LMsk", b"Lr16", b"Lr32", b"Layr", b"Mt16", b"Mt32", b"Mtrn", b"Alph", b"FMsk", b"lnk2",
    b"FEid", b"FXid", b"PxSD", b"lnkE", b"lnk3", b"lnkD",
];

const ADJUSTMENTS: &[(&[u8; 4], &str)] = &[
    (b"levl", "Levels"),
    (b"curv", "Curves"),
    (b"brit", "Brightness/Contrast"),
    (b"blnc", "Color Balance"),
    (b"hue2", "Hue/Saturation"),
    (b"hue ", "Hue/Saturation"),
    (b"selc", "Selective Color"),
    (b"thrs", "Threshold"),
    (b"nvrt", "Invert"),
    (b"post", "Posterize"),
    (b"mixr", "Channel Mixer"),
    (b"grdm", "Gradient Map"),
    (b"phfl", "Photo Filter"),
    (b"expA", "Exposure"),
    (b"vibA", "Vibrance"),
    (b"clrL", "Color Lookup"),
    (b"blwh", "Black & White"),
];

/// One layer record as saved, before the stack is put together.
struct Record {
    layer: Layer,
    /// Group marker: 1 or 2 opens a group (its folder), 3 closes one.
    section: u32,
    channels: Vec<(i16, usize)>,
    has_lyid: bool,
}

pub(crate) fn parse(bytes: &[u8]) -> Result<Document> {
    let mut r = Reader::new(bytes);
    if r.take(4)? != b"8BPS" {
        return Err("not a Photoshop document".into());
    }
    let wide = match r.u16()? {
        1 => false,
        2 => true,
        v => return Err(format!("an unknown Photoshop file version {v}")),
    };
    r.skip(6)?;
    let channels = r.u16()?;
    let height = r.u32()?;
    let width = r.u32()?;
    let depth = r.u16()?;
    let mode = r.u16()?;
    let mut doc = Document {
        wide,
        width,
        height,
        depth,
        mode,
        channels,
        resolution: None,
        guides: Vec::new(),
        layers: Vec::new(),
        merged_alpha: false,
        composite_at: None,
        embedded: BTreeMap::new(),
        palette: Vec::new(),
    };

    let n = r.u32()? as usize;
    doc.palette = r.take(n)?.to_vec(); // colour mode data: an indexed image's palette
    let n = r.u32()? as usize;
    resources(&mut r.sub(n)?, &mut doc)?;

    let n = r.len(wide)?;
    let mut info = r.sub(n)?;
    doc.composite_at = Some(r.pos());
    layer_and_mask(&mut info, &mut doc)?;
    Ok(doc)
}

/// The image resources the diff reports: resolution and guides.
fn resources(r: &mut Reader, doc: &mut Document) -> Result<()> {
    while r.left() >= 12 {
        if r.take(4)? != b"8BIM" {
            break;
        }
        let id = r.u16()?;
        let name = r.u8()? as usize;
        r.skip(name + (name + 1) % 2)?;
        let n = r.u32()? as usize;
        let mut data = r.sub(n)?;
        if n % 2 == 1 && r.left() > 0 {
            r.skip(1)?;
        }
        match id {
            1005 if n >= 4 => doc.resolution = Some(f64::from(data.u32()?) / 65536.0),
            1032 if n >= 16 => {
                data.skip(12)?; // version, grid cycle
                let count = data.u32()?;
                for _ in 0..count.min(10_000) {
                    let at = f64::from(data.i32()?) / 32.0;
                    let axis = if data.u8()? == 0 { "x" } else { "y" };
                    doc.guides.push(format!("{axis} {}", round2(at)));
                }
            }
            _ => {}
        }
    }
    Ok(())
}

fn layer_and_mask(r: &mut Reader, doc: &mut Document) -> Result<()> {
    if r.left() == 0 {
        return Ok(());
    }
    let n = r.len(doc.wide)?;
    let mut layers = r.sub(n)?;
    let mut records = if n > 0 {
        layer_info(&mut layers, doc)?
    } else {
        Vec::new()
    };
    // Global layer mask info.
    if r.left() >= 4 {
        let n = r.u32()? as usize;
        r.skip(n)?;
    }
    // Global blocks: 16- and 32-bit documents keep their layers here, and
    // smart objects' embedded files are here too.
    while r.left() >= 12 {
        let sig = r.key()?;
        if &sig != b"8BIM" && &sig != b"8B64" {
            break;
        }
        let key = r.key()?;
        let n = r.len(doc.wide && WIDE_KEYS.contains(&&key))?;
        let mut data = r.sub(n)?;
        to_next_block(r);
        match &key {
            b"Lr16" | b"Lr32" if records.is_empty() => records = layer_info(&mut data, doc)?,
            b"lnk2" | b"lnkD" | b"lnk3" => embedded(&mut data, doc)?,
            _ => {}
        }
    }
    stack(records, doc);
    Ok(())
}

/// Past the padding after a block (blocks are padded to an even length, some
/// writers to four bytes): up to the next block's signature.
fn to_next_block(r: &mut Reader) {
    for _ in 0..3 {
        let mut peek = r.clone();
        match peek.key() {
            Ok(sig) if &sig == b"8BIM" || &sig == b"8B64" => return,
            _ => {}
        }
        if r.clone().u8().ok() != Some(0) || r.skip(1).is_err() {
            return;
        }
    }
}

/// The layer records and their channels.
fn layer_info(r: &mut Reader, doc: &mut Document) -> Result<Vec<Record>> {
    let count = r.i16()?;
    doc.merged_alpha = count < 0;
    let count = count.unsigned_abs() as usize;
    let mut records = Vec::with_capacity(count.min(10_000));
    for i in 0..count {
        records.push(record(r, doc, i)?);
    }
    for rec in &mut records {
        let mut colour = Vec::new();
        let mut mask = Vec::new();
        for (id, n) in &rec.channels {
            let data = r.take(*n)?;
            if *id >= -1 {
                colour.push(fingerprint(&[&id.to_be_bytes(), data]).to_be_bytes());
            } else {
                mask.push(fingerprint(&[&id.to_be_bytes(), data]).to_be_bytes());
            }
        }
        let parts: Vec<&[u8]> = colour.iter().map(|c| c.as_slice()).collect();
        rec.layer.pixels = fingerprint(&parts);
        if let Some(m) = &mut rec.layer.mask {
            let parts: Vec<&[u8]> = mask.iter().map(|c| c.as_slice()).collect();
            m.pixels = fingerprint(&parts);
        }
    }
    Ok(records)
}

fn record(r: &mut Reader, doc: &Document, index: usize) -> Result<Record> {
    let top = r.i32()?;
    let left = r.i32()?;
    let bottom = r.i32()?;
    let right = r.i32()?;
    let n = r.u16()? as usize;
    let mut channels = Vec::with_capacity(n.min(64));
    for _ in 0..n {
        let id = r.i16()?;
        let len = r.len(doc.wide)?;
        channels.push((id, len));
    }
    if r.take(4)? != b"8BIM" {
        return Err(format!("layer {index}: a damaged layer record"));
    }
    let blend = r.key()?;
    let opacity = r.u8()?;
    let clipped = r.u8()? != 0;
    let flags = r.u8()?;
    r.skip(1)?;
    let n = r.u32()? as usize;
    let mut extra = r.sub(n)?;

    let n = extra.u32()? as usize;
    let mut mask_data = extra.sub(n)?;
    let n = extra.u32()? as usize;
    extra.skip(n)?; // blending ranges
    let name_len = extra.u8()? as usize;
    let name = extra.take(name_len)?;
    let mut name: String = name.iter().map(|b| char::from(*b)).collect();
    extra.skip((4 - (name_len + 1) % 4) % 4)?;

    let rect = Rect {
        left: f64::from(left),
        top: f64::from(top),
        right: f64::from(right),
        bottom: f64::from(bottom),
    };
    let mut layer = Layer {
        id: 0,
        name: String::new(),
        kind: Kind::Pixel,
        parent: None,
        rect,
        visible: flags & 2 == 0,
        opacity,
        fill_opacity: 255,
        blend,
        clipped,
        locks: 0,
        label: 0,
        pixels: 0,
        mask: None,
        vector: None,
        text: None,
        placed: None,
        fill: None,
        stroke: None,
        effects: None,
        settings: None,
    };
    if channels.iter().any(|(id, _)| *id <= -2) && mask_data.left() >= 18 {
        mask_data.skip(16)?; // its bounds
        let _default = mask_data.u8()?;
        let flags = mask_data.u8()?;
        // Bit 3: drawn from other data (a shape's vector mask, rendered):
        // not a mask of its own, and it moves with the layer.
        if flags & 8 == 0 {
            layer.mask = Some(Mask {
                disabled: flags & 2 != 0,
                pixels: 0,
            });
        }
    }

    let mut section = 0;
    let mut lyid = None;
    let (mut has_fill, mut has_vector) = (false, false);
    while extra.left() >= 12 {
        let sig = extra.key()?;
        if &sig != b"8BIM" && &sig != b"8B64" {
            break;
        }
        let key = extra.key()?;
        let n = extra.len(doc.wide && WIDE_KEYS.contains(&&key))?;
        let mut data = extra.sub(n)?;
        to_next_block(&mut extra);
        let block = data.clone();
        match &key {
            b"luni" => name = data.unicode()?,
            b"lyid" => lyid = Some(data.u32()?),
            b"lsct" | b"lsdk" => section = data.u32()?,
            b"iOpa" => layer.fill_opacity = data.u8()?,
            b"lspf" => layer.locks = data.u32()?,
            b"lclr" => layer.label = data.u16()?,
            b"TySh" => layer.text = Some(text(&mut data)?),
            b"SoLd" | b"SoLE" => layer.placed = Some(smart(&mut data)?),
            b"PlLd" if layer.placed.is_none() => layer.placed = Some(placed(&mut data)?),
            b"SoCo" | b"GdFl" | b"PtFl" => {
                data.u32()?;
                layer.fill = Some(descriptor::descriptor(&mut data)?);
                has_fill = true;
            }
            b"vscg" if layer.fill.is_none() => {
                data.u32()?;
                data.u32()?;
                layer.fill = Some(descriptor::descriptor(&mut data)?);
                has_fill = true;
            }
            b"vstk" => {
                data.u32()?;
                layer.stroke = Some(descriptor::descriptor(&mut data)?);
            }
            b"vmsk" | b"vsms" => {
                layer.vector = Some(vector(&mut data, doc)?);
                has_vector = true;
            }
            b"lfx2" | b"lmfx" => {
                data.u32()?;
                data.u32()?;
                layer.effects = Some(descriptor::descriptor(&mut data)?);
            }
            k => {
                if let Some((_, name)) = ADJUSTMENTS.iter().find(|(a, _)| *a == k) {
                    layer.kind = Kind::Adjustment(name);
                    layer.settings = Some(fingerprint(&[block.clone().take(n)?]));
                }
            }
        }
    }

    layer.kind = match () {
        _ if matches!(section, 1 | 2) => Kind::Group,
        _ if layer.text.is_some() => Kind::Text,
        _ if layer.placed.is_some() => Kind::Smart,
        _ if has_fill && has_vector => Kind::Shape,
        _ if has_fill => Kind::Fill,
        _ => layer.kind,
    };
    layer.name = name;
    layer.id = lyid.unwrap_or(0);
    Ok(Record {
        layer,
        section,
        channels,
        has_lyid: lyid.is_some(),
    })
}

/// Records come bottom of the stack first, a group closed by a marker below
/// its layers and opened by its folder record above them. Turned around: top
/// first, each layer knowing its group.
fn stack(records: Vec<Record>, doc: &mut Document) {
    let mut open: Vec<u32> = Vec::new();
    let mut seen: BTreeMap<String, u32> = BTreeMap::new();
    for rec in records.into_iter().rev() {
        if rec.section == 3 {
            open.pop();
            continue;
        }
        let mut layer = rec.layer;
        if !rec.has_lyid {
            // No id saved: the name (and which of its namesakes) stands in.
            let nth = seen.entry(layer.name.clone()).or_insert(0);
            *nth += 1;
            let made = fingerprint(&[layer.name.as_bytes(), &nth.to_be_bytes()]);
            layer.id = 0x8000_0000 | (made as u32 & 0x7fff_ffff);
        }
        layer.parent = open.last().copied();
        if layer.kind == Kind::Group {
            open.push(layer.id);
        }
        doc.layers.push(layer);
    }
}

/// A text layer: its text, where it sits, and its styling.
fn text(r: &mut Reader) -> Result<Text> {
    r.u16()?;
    let mut transform = [0.0; 6];
    for t in &mut transform {
        *t = r.f64()?;
    }
    r.u16()?;
    r.u32()?;
    let d = descriptor::descriptor(r)?;
    let warp = (|| -> Result<Option<String>> {
        r.u16()?;
        r.u32()?;
        let w = descriptor::descriptor(r)?;
        Ok(w.get("warpStyle")
            .and_then(Value::as_str)
            .map(|s| s.rsplit('.').next().unwrap_or(s).to_string())
            .filter(|s| s != "warpNone"))
    })()
    .unwrap_or(None);
    let content = d
        .get("Txt ")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .replace('\r', "\n")
        .trim_end_matches('\n')
        .to_string();
    let engine = d.get("EngineData").cloned().unwrap_or(Value::Null);
    Ok(Text {
        content,
        transform,
        runs: runs(&engine),
        align: align(&engine),
        warp,
    })
}

/// Each style run's look: the run's own settings over the document defaults.
fn runs(e: &Value) -> Vec<Style> {
    let fonts: Vec<String> = e
        .pointer("/ResourceDict/FontSet")
        .or_else(|| e.pointer("/DocumentResources/FontSet"))
        .and_then(Value::as_array)
        .map(|a| {
            a.iter()
                .map(|f| {
                    f.get("Name")
                        .and_then(Value::as_str)
                        .unwrap_or("")
                        .to_string()
                })
                .collect()
        })
        .unwrap_or_default();
    let defaults = e
        .pointer("/ResourceDict/StyleSheetSet/0/StyleSheetData")
        .or_else(|| e.pointer("/DocumentResources/StyleSheetSet/0/StyleSheetData"));
    let style = |sheet: Option<&Value>| -> Style {
        let get = |k: &str| {
            sheet
                .and_then(|s| s.get(k))
                .or_else(|| defaults.and_then(|d| d.get(k)))
        };
        let f = |k: &str| get(k).and_then(Value::as_f64);
        let b = |k: &str| get(k).and_then(Value::as_bool).unwrap_or(false);
        Style {
            font: f("Font")
                .and_then(|i| fonts.get(i as usize))
                .cloned()
                .filter(|s| !s.is_empty()),
            size: f("FontSize"),
            colour: get("FillColor").and_then(engine_colour),
            tracking: f("Tracking"),
            leading: if b("AutoLeading") { None } else { f("Leading") },
            bold: b("FauxBold"),
            italic: b("FauxItalic"),
            underline: b("Underline"),
        }
    };
    let runs: Vec<Style> = e
        .pointer("/EngineDict/StyleRun/RunArray")
        .and_then(Value::as_array)
        .map(|a| {
            a.iter()
                .map(|run| style(run.pointer("/StyleSheet/StyleSheetData")))
                .collect()
        })
        .unwrap_or_default();
    let mut runs = if runs.is_empty() {
        vec![style(None)]
    } else {
        runs
    };
    // Some writers save a run per character: neighbours alike are one run.
    runs.dedup();
    runs
}

/// An EngineData colour: `<< /Type 1 /Values [ a r g b ] >>`, RGB in 0–1.
fn engine_colour(c: &Value) -> Option<[f64; 4]> {
    let v = c.get("Values")?.as_array()?;
    let n = |i: usize| v.get(i).and_then(Value::as_f64);
    // 1: RGB; 0 with two values: grey.
    match c.get("Type").and_then(Value::as_f64).map(|t| t as i64) {
        Some(1) => Some([n(1)?, n(2)?, n(3)?, n(0)?]),
        Some(0) if v.len() == 2 => Some([n(1)?, n(1)?, n(1)?, n(0)?]),
        _ => None,
    }
}

fn align(e: &Value) -> Option<&'static str> {
    let j = e
        .pointer("/EngineDict/ParagraphRun/RunArray/0/ParagraphSheet/Properties/Justification")
        .or_else(|| e.pointer("/ResourceDict/ParagraphSheetSet/0/Properties/Justification"))?
        .as_f64()?;
    Some(match j as i64 {
        0 => "left",
        1 => "right",
        2 => "centre",
        3 => "justified, last line left",
        4 => "justified, last line right",
        5 => "justified, last line centred",
        _ => "justified",
    })
}

/// A smart object (`SoLd`): where its content sits, and which file it shows.
fn smart(r: &mut Reader) -> Result<Placed> {
    r.key()?; // "soLD"
    r.u32()?;
    r.u32()?;
    let d = descriptor::descriptor(r)?;
    let quad = d
        .get("Trnf")
        .and_then(Value::as_array)
        .filter(|a| a.len() == 8)
        .map(|a| {
            let mut q = [0.0; 8];
            for (i, v) in a.iter().enumerate() {
                q[i] = v.as_f64().unwrap_or(0.0);
            }
            q
        })
        .ok_or("a smart object without its transform")?;
    Ok(Placed {
        quad,
        source: d
            .get("Idnt")
            .or_else(|| d.get("placed"))
            .and_then(Value::as_str)
            .map(str::to_string),
    })
}

/// An older placed layer (`PlLd`), where there is no `SoLd`.
fn placed(r: &mut Reader) -> Result<Placed> {
    r.key()?; // "plcL"
    r.u32()?;
    let source = r.pascal()?;
    r.skip(16)?; // page, pages, anti-alias, layer type
    let mut quad = [0.0; 8];
    for q in &mut quad {
        *q = r.f64()?;
    }
    Ok(Placed {
        quad,
        source: Some(source),
    })
}

/// A vector mask: its path's bounds on the canvas and a fingerprint of it.
fn vector(r: &mut Reader, doc: &Document) -> Result<Vector> {
    r.u32()?;
    let flags = r.u32()?;
    let path = r.clone().take(r.left())?;
    let (w, h) = (f64::from(doc.width), f64::from(doc.height));
    let mut rect: Option<Rect> = None;
    while r.left() >= 26 {
        let kind = r.u16()?;
        if matches!(kind, 1 | 2 | 4 | 5) {
            for _ in 0..3 {
                let y = f64::from(r.i32()?) / f64::from(1 << 24) * h;
                let x = f64::from(r.i32()?) / f64::from(1 << 24) * w;
                let p = Rect {
                    left: x,
                    top: y,
                    right: x,
                    bottom: y,
                };
                rect = Some(match rect {
                    None => p,
                    Some(r) => Rect {
                        left: r.left.min(x),
                        top: r.top.min(y),
                        right: r.right.max(x),
                        bottom: r.bottom.max(y),
                    },
                });
            }
        } else {
            r.skip(24)?;
        }
    }
    Ok(Vector {
        rect: rect.unwrap_or(Rect {
            left: 0.0,
            top: 0.0,
            right: w,
            bottom: h,
        }),
        path: fingerprint(&[path]),
        disabled: flags & 4 != 0,
    })
}

/// Smart objects' embedded files: each item's unique id, and a fingerprint of
/// the file.
fn embedded(r: &mut Reader, doc: &mut Document) -> Result<()> {
    while r.left() >= 8 {
        let n = r.u64()? as usize;
        let mut item = r.sub(n)?;
        let all = item.clone().take(item.left())?;
        while r.left() > 0 && r.pos() % 4 != 0 {
            r.skip(1)?;
        }
        item.key()?; // liFD, liFE, liFA
        item.u32()?;
        let id = item.pascal()?;
        doc.embedded.insert(id, fingerprint(&[all]));
    }
    Ok(())
}

pub(crate) fn round2(x: f64) -> f64 {
    (x * 100.0).round() / 100.0
}
