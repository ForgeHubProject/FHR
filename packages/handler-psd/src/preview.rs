//! `preview`: the document as Photoshop flattened it (the image a PSD keeps
//! after its layers), as a PNG with a `tEXt` chunk ([`CHUNK_KEYWORD`]) saying
//! where each layer is on it, by layer id. The renderer compares the two
//! images and outlines the layers the diff names.

use serde_json::{json, Map, Value};

use crate::bytes::{Reader, Result};
use crate::file::{self, Document, Kind, Rect};
use crate::values::bounds;

/// The keyword of the PNG `tEXt` chunk holding the layers' boxes.
pub const CHUNK_KEYWORD: &str = "fhr-psd";

/// The most pixels a preview holds; a larger document is drawn smaller.
const MAX_PIXELS: u64 = 16_000_000;

pub(crate) fn sheet(bytes: &[u8]) -> Result<Vec<u8>> {
    let doc = file::parse(bytes)?;
    let at = doc.composite_at.ok_or("the file has no flattened image")?;
    let mut r = Reader::new(bytes.get(at..).ok_or("the file has no flattened image")?);
    let planes = composite(&mut r, &doc)?;
    let rgba = to_rgba(&doc, &planes)?;
    let (w, h, rgba, scale) = shrink(doc.width, doc.height, rgba);
    encode(w, h, &rgba, &layout(&doc, scale, w, h))
}

/// The flattened image's channels, each as 8-bit samples.
fn composite(r: &mut Reader, doc: &Document) -> Result<Vec<Vec<u8>>> {
    let (w, h) = (doc.width as usize, doc.height as usize);
    if w == 0 || h == 0 {
        return Err("an empty canvas".into());
    }
    let channels = doc.channels as usize;
    let row = (w * doc.depth as usize).div_ceil(8);
    let plane = row.checked_mul(h).ok_or("a canvas larger than memory")?;
    let compression = r.u16()?;
    let raw: Vec<u8> = match compression {
        0 => r
            .take(
                plane
                    .checked_mul(channels)
                    .ok_or("a canvas larger than memory")?,
            )?
            .to_vec(),
        1 => {
            let rows = channels * h;
            let mut counts = Vec::with_capacity(rows);
            for _ in 0..rows {
                counts.push(if doc.wide {
                    r.u32()? as usize
                } else {
                    r.u16()? as usize
                });
            }
            let mut out = Vec::with_capacity(plane * channels);
            for n in counts {
                unpack_bits(r.take(n)?, row, &mut out);
            }
            out
        }
        2 | 3 => {
            let rest = r.take(r.left())?;
            let mut out =
                miniz_oxide::inflate::decompress_to_vec_zlib_with_limit(rest, plane * channels)
                    .map_err(|e| format!("the flattened image doesn't inflate: {e:?}"))?;
            if compression == 3 {
                unpredict(&mut out, row, doc.depth);
            }
            out
        }
        c => {
            return Err(format!(
                "an unknown compression ({c}) for the flattened image"
            ))
        }
    };
    if raw.len() < plane * channels {
        return Err("the flattened image ends early".into());
    }
    Ok(raw
        .chunks_exact(plane)
        .take(channels)
        .map(|p| eight_bit(p, doc.depth, w))
        .collect())
}

/// PackBits: one run of `row` bytes.
fn unpack_bits(src: &[u8], row: usize, out: &mut Vec<u8>) {
    let start = out.len();
    let mut i = 0;
    while i < src.len() && out.len() - start < row {
        let n = src[i] as i8;
        i += 1;
        if n >= 0 {
            let len = n as usize + 1;
            let end = (i + len).min(src.len());
            out.extend_from_slice(&src[i..end]);
            i = end;
        } else if n != -128 {
            if let Some(b) = src.get(i) {
                out.extend(std::iter::repeat_n(*b, (1 - isize::from(n)) as usize));
            }
            i += 1;
        }
    }
    out.resize(start + row, 0);
}

/// ZIP with prediction: each row stored as differences from the sample before.
fn unpredict(data: &mut [u8], row: usize, depth: u16) {
    for r in data.chunks_mut(row) {
        match depth {
            16 => {
                let mut prev = 0u16;
                for c in r.chunks_exact_mut(2) {
                    let v = u16::from_be_bytes([c[0], c[1]]).wrapping_add(prev);
                    c.copy_from_slice(&v.to_be_bytes());
                    prev = v;
                }
            }
            _ => {
                for i in 1..r.len() {
                    r[i] = r[i].wrapping_add(r[i - 1]);
                }
            }
        }
    }
}

/// One channel at `depth` bits as 8-bit samples.
fn eight_bit(p: &[u8], depth: u16, width: usize) -> Vec<u8> {
    match depth {
        16 => p.chunks_exact(2).map(|c| c[0]).collect(),
        32 => p
            .chunks_exact(4)
            .map(|c| {
                let v = f32::from_be_bytes([c[0], c[1], c[2], c[3]]).clamp(0.0, 1.0);
                (v.powf(1.0 / 2.2) * 255.0).round() as u8
            })
            .collect(),
        1 => {
            let row = width.div_ceil(8);
            p.chunks(row)
                .flat_map(|r| {
                    (0..width).map(move |x| {
                        if r[x / 8] & (0x80 >> (x % 8)) != 0 {
                            0
                        } else {
                            255
                        }
                    })
                })
                .collect()
        }
        _ => p.to_vec(),
    }
}

fn to_rgba(doc: &Document, planes: &[Vec<u8>]) -> Result<Vec<u8>> {
    let n = doc.width as usize * doc.height as usize;
    let plane = |i: usize| planes.get(i).filter(|p| p.len() >= n);
    let opaque = vec![255u8; n];
    let mut out = Vec::with_capacity(n * 4);
    match doc.mode {
        3 => {
            let (r, g, b) = (
                plane(0).ok_or("no red channel")?,
                plane(1).ok_or("no green channel")?,
                plane(2).ok_or("no blue channel")?,
            );
            let a = if doc.merged_alpha {
                plane(3).unwrap_or(&opaque)
            } else {
                &opaque
            };
            for i in 0..n {
                out.extend_from_slice(&[r[i], g[i], b[i], a[i]]);
            }
        }
        4 => {
            let ink: Vec<&Vec<u8>> = (0..4)
                .map(|i| plane(i).ok_or_else(|| "a CMYK channel is missing".to_string()))
                .collect::<Result<_>>()?;
            let a = if doc.merged_alpha {
                plane(4).unwrap_or(&opaque)
            } else {
                &opaque
            };
            for i in 0..n {
                // Saved inverted: 255 is no ink.
                let k = u16::from(ink[3][i]);
                let c = |p: &Vec<u8>| (u16::from(p[i]) * k / 255) as u8;
                out.extend_from_slice(&[c(ink[0]), c(ink[1]), c(ink[2]), a[i]]);
            }
        }
        // Grayscale, bitmap, duotone, multichannel and Lab: the first channel
        // (Lab's lightness), as grey.
        0 | 1 | 7 | 8 | 9 => {
            let g = plane(0).ok_or("no channel")?;
            let a = if doc.merged_alpha && doc.mode == 1 {
                plane(1).unwrap_or(&opaque)
            } else {
                &opaque
            };
            for i in 0..n {
                out.extend_from_slice(&[g[i], g[i], g[i], a[i]]);
            }
        }
        2 => {
            let idx = plane(0).ok_or("no channel")?;
            let pal = &doc.palette;
            if pal.len() < 768 {
                return Err("an indexed image without its palette".into());
            }
            for k in idx.iter().take(n).map(|k| usize::from(*k)) {
                out.extend_from_slice(&[pal[k], pal[256 + k], pal[512 + k], 255]);
            }
        }
        m => return Err(format!("no preview for colour mode {m}")),
    }
    Ok(out)
}

/// The image smaller by a whole factor when it is past [`MAX_PIXELS`] (each
/// pixel the mean of the block it stands for), and that factor's inverse.
fn shrink(w: u32, h: u32, rgba: Vec<u8>) -> (u32, u32, Vec<u8>, f64) {
    let area = u64::from(w) * u64::from(h);
    if area <= MAX_PIXELS {
        return (w, h, rgba, 1.0);
    }
    let step = ((area as f64 / MAX_PIXELS as f64).sqrt().ceil() as u32).max(2);
    let (nw, nh) = (w.div_ceil(step), h.div_ceil(step));
    let mut out = Vec::with_capacity(nw as usize * nh as usize * 4);
    for y in 0..nh {
        for x in 0..nw {
            let mut sum = [0u32; 4];
            let mut count = 0;
            for yy in y * step..((y + 1) * step).min(h) {
                for xx in x * step..((x + 1) * step).min(w) {
                    let i = (yy as usize * w as usize + xx as usize) * 4;
                    for c in 0..4 {
                        sum[c] += u32::from(rgba[i + c]);
                    }
                    count += 1;
                }
            }
            out.extend(sum.iter().map(|s| (s / count.max(1)) as u8));
        }
    }
    (nw, nh, out, 1.0 / f64::from(step))
}

/// Where each layer is on the preview: `{v, w, h, scale, layers: {id: [x, y, w, h]}}`.
/// A group's box holds its layers'.
fn layout(doc: &Document, scale: f64, w: u32, h: u32) -> String {
    let canvas = Rect {
        left: 0.0,
        top: 0.0,
        right: f64::from(doc.width),
        bottom: f64::from(doc.height),
    };
    let mut boxes: Vec<(u32, Rect)> = Vec::new();
    for l in &doc.layers {
        let r = match l.kind {
            Kind::Group => continue,
            Kind::Adjustment(_) | Kind::Fill if bounds(l).is_empty() => canvas,
            _ => bounds(l),
        };
        if r.is_empty() {
            continue;
        }
        boxes.push((l.id, r));
        // Every group it's in holds it too.
        let mut parent = l.parent;
        for _ in 0..64 {
            let Some(p) = parent else { break };
            match boxes.iter_mut().find(|(id, _)| *id == p) {
                Some((_, b)) => *b = b.union(r),
                None => boxes.push((p, r)),
            }
            parent = doc.layers.iter().find(|g| g.id == p).and_then(|g| g.parent);
        }
    }
    let mut layers = Map::new();
    let s = |v: f64| (v * scale * 10.0).round() / 10.0;
    for (id, r) in boxes {
        layers.insert(
            id.to_string(),
            json!([s(r.left), s(r.top), s(r.width()), s(r.height())]),
        );
    }
    json!({ "v": 1, "w": w, "h": h, "scale": scale, "layers": Value::Object(layers) }).to_string()
}

fn encode(w: u32, h: u32, rgba: &[u8], layout: &str) -> Result<Vec<u8>> {
    let mut out = Vec::new();
    {
        let mut enc = png::Encoder::new(&mut out, w, h);
        enc.set_color(png::ColorType::Rgba);
        enc.set_depth(png::BitDepth::Eight);
        // Made on demand and cached by the host: speed over size.
        enc.set_compression(png::Compression::Fast);
        enc.add_text_chunk(CHUNK_KEYWORD.to_string(), layout.to_string())
            .map_err(|e| e.to_string())?;
        let mut writer = enc.write_header().map_err(|e| e.to_string())?;
        writer.write_image_data(rgba).map_err(|e| e.to_string())?;
    }
    Ok(out)
}
