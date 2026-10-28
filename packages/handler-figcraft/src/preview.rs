//! `preview`: what a browser draws for a `.figcraft` file. One PNG, a sheet of
//! every screen (the screens the diff groups by), each drawn by FigCraft's own
//! renderer at its true size, plus a `tEXt` chunk ([`CHUNK_KEYWORD`]) saying
//! where each screen is on the sheet and where each of its layers is on the
//! screen. The renderer cuts a screen out of the sheets of both sides and
//! outlines the layers the diff names by their ids.
//!
//! Text is drawn with the fonts compiled in (Inter, see fonts.sh), never the
//! machine's own: the same file draws the same on the server, in a browser and
//! in forge.

use figcraft_doc::{Document, ExportFormat, NodeId};
use figcraft_render::Pixmap;
use serde_json::{json, Map, Value};

use crate::tree::screen_of;

/// The keyword of the PNG `tEXt` chunk holding the sheet's layout.
pub const CHUNK_KEYWORD: &str = "fhr-figcraft";

/// The most pixels a sheet holds; a larger file is drawn smaller.
const MAX_PIXELS: f64 = 16_000_000.0;
/// A sheet row's width: screens wrap onto the next row past it.
const ROW_WIDTH: u32 = 4096;
/// The longest side FigCraft renders.
const MAX_SIDE: f64 = 16_384.0;

/// One screen on the sheet.
struct Screen {
    id: NodeId,
    page: NodeId,
    layers: Vec<NodeId>,
}

/// The sheet of `doc`'s screens, as a PNG with its layout chunk.
pub(crate) fn sheet(doc: &Document) -> Result<Vec<u8>, String> {
    figcraft_text::opt_out_of_system_fonts();
    let screens = screens(doc);
    let bounds: Vec<_> = screens
        .iter()
        .map(|s| figcraft_render::export_bounds(doc, s.id))
        .collect();
    // One scale for the whole sheet, so every screen keeps its true proportions
    // to the others: 1 unless the file is too large to draw at full size.
    let (area, longest) = bounds.iter().flatten().fold((0.0, 0.0f64), |(a, l), b| {
        (a + b.width() * b.height(), l.max(b.width()).max(b.height()))
    });
    let mut scale = 1.0f64;
    if area > MAX_PIXELS {
        scale = (MAX_PIXELS / area).sqrt();
    }
    if longest * scale > MAX_SIDE {
        scale = MAX_SIDE / longest;
    }

    let mut drawn: Vec<(usize, Pixmap)> = Vec::new();
    for (i, s) in screens.iter().enumerate() {
        if let Some(p) = figcraft_render::render_node(doc, s.id, scale) {
            drawn.push((i, p));
        }
    }
    // Shelf packing: left to right, wrapping past ROW_WIDTH (or the widest screen).
    let row = drawn
        .iter()
        .map(|(_, p)| p.width())
        .max()
        .unwrap_or(1)
        .max(ROW_WIDTH);
    let (mut x, mut y, mut shelf, mut width) = (0u32, 0u32, 0u32, 1u32);
    let mut places = Vec::with_capacity(drawn.len());
    for (_, p) in &drawn {
        if x > 0 && x + p.width() > row {
            x = 0;
            y += shelf;
            shelf = 0;
        }
        places.push((x, y));
        x += p.width();
        shelf = shelf.max(p.height());
        width = width.max(x);
    }
    let height = (y + shelf).max(1);
    let mut sheet = Pixmap::new(width, height).ok_or("the file is too large to preview")?;
    for ((_, p), (px, py)) in drawn.iter().zip(&places) {
        blit(&mut sheet, p, *px, *py);
    }

    let mut layout = Vec::with_capacity(drawn.len());
    for ((i, p), (px, py)) in drawn.iter().zip(&places) {
        let (Some(s), Some(Some(origin))) = (screens.get(*i), bounds.get(*i)) else {
            continue;
        };
        let mut boxes = Map::new();
        for id in &s.layers {
            if let Some(b) = doc.world_bounds(*id) {
                let r = |v: f64| (v * scale * 10.0).round() / 10.0;
                boxes.insert(
                    id.to_string(),
                    json!([
                        r(b.x0 - origin.x0),
                        r(b.y0 - origin.y0),
                        r(b.width()),
                        r(b.height())
                    ]),
                );
            }
        }
        let name = |id: NodeId| doc.node(id).map_or("", |n| n.name.as_str()).to_string();
        layout.push(json!({
            "id": s.id.0,
            "name": name(s.id),
            "page": s.page.0,
            "pageName": name(s.page),
            "x": px, "y": py, "w": p.width(), "h": p.height(),
            "layers": Value::Object(boxes),
        }));
    }
    let manifest = json!({ "v": 1, "scale": scale, "screens": layout });

    let png = figcraft_render::encode_export(&sheet, ExportFormat::Png, doc.color_profile)?;
    let mut chunk = CHUNK_KEYWORD.as_bytes().to_vec();
    chunk.push(0);
    chunk.extend(ascii_json(&manifest).into_bytes());
    Ok(figcraft_render::png_insert_chunk(png, b"tEXt", &chunk))
}

/// Every screen of every page in document order, each with its layers.
fn screens(doc: &Document) -> Vec<Screen> {
    let mut out: Vec<Screen> = Vec::new();
    for page in &doc.pages {
        for id in doc.subtree(*page) {
            let Some(screen) = screen_of(doc, id) else {
                continue;
            };
            match out.iter_mut().find(|s| s.id == screen) {
                Some(s) => s.layers.push(id),
                None => out.push(Screen {
                    id: screen,
                    page: *page,
                    layers: vec![id],
                }),
            }
        }
    }
    out
}

/// Copy `src` into `dst` with its top-left at (x, y). Both are premultiplied
/// RGBA, and the sheet starts transparent, so a copy is the composite.
fn blit(dst: &mut Pixmap, src: &Pixmap, x: u32, y: u32) {
    let (dw, sw) = (dst.width() as usize, src.width() as usize);
    let rows = src.height().min(dst.height().saturating_sub(y)) as usize;
    let cols = sw.min(dw.saturating_sub(x as usize));
    let data = dst.data_mut();
    for r in 0..rows {
        let from = r * sw * 4;
        let to = ((y as usize + r) * dw + x as usize) * 4;
        if let (Some(d), Some(s)) = (
            data.get_mut(to..to + cols * 4),
            src.data().get(from..from + cols * 4),
        ) {
            d.copy_from_slice(s);
        }
    }
}

/// JSON with every non-ASCII character escaped: a `tEXt` chunk is Latin-1, and
/// escaped JSON reads back the same.
fn ascii_json(v: &Value) -> String {
    let text = v.to_string();
    let mut out = String::with_capacity(text.len());
    for c in text.chars() {
        if c.is_ascii() {
            out.push(c);
        } else {
            let mut units = [0u16; 2];
            for u in c.encode_utf16(&mut units) {
                out.push_str(&format!("\\u{u:04x}"));
            }
        }
    }
    out
}
