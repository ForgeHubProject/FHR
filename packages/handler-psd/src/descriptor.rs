//! Photoshop's action descriptors: the typed key–value trees that text layers,
//! smart objects, fill layers, vector strokes and layer effects are saved as.
//!
//! Read into JSON so the rest of the handler can look values up by key: a
//! descriptor is an object (its class under `"_class"`), a unit value
//! `{"_unit": "#Pxl", "value": 12}`, an enumeration `"Type.value"`, raw data
//! a fingerprint `{"_bytes": n, "_hash": "…"}` — except a text layer's
//! EngineData, which is read as well (engine.rs). Keys keep Photoshop's own
//! four-character spelling, trailing spaces included (`"Txt "`, `"Clr "`).

use serde_json::{json, Map, Value};

use crate::bytes::{fingerprint, Reader, Result};
use crate::engine;

/// Deeper than any descriptor Photoshop writes; a file nesting further is damaged.
const MAX_DEPTH: usize = 32;

/// A descriptor: its name, class and items.
pub(crate) fn descriptor(r: &mut Reader) -> Result<Value> {
    descriptor_at(r, 0)
}

fn descriptor_at(r: &mut Reader, depth: usize) -> Result<Value> {
    if depth > MAX_DEPTH {
        return Err("descriptors nested too deep".into());
    }
    let _name = r.unicode()?;
    let class = id(r)?;
    let count = r.u32()?;
    let mut map = Map::new();
    map.insert("_class".into(), Value::String(class));
    for _ in 0..count {
        let key = id(r)?;
        let kind = r.key()?;
        let value = if key == "EngineData" && &kind == b"tdta" {
            let n = r.u32()? as usize;
            engine::parse(r.take(n)?)?
        } else {
            item(r, kind, depth)?
        };
        map.insert(key, value);
    }
    Ok(Value::Object(map))
}

/// A class or key id: four characters, or a longer name with its length first.
fn id(r: &mut Reader) -> Result<String> {
    let n = r.u32()? as usize;
    let raw = r.take(if n == 0 { 4 } else { n })?;
    Ok(String::from_utf8_lossy(raw).into_owned())
}

fn item(r: &mut Reader, kind: [u8; 4], depth: usize) -> Result<Value> {
    if depth > MAX_DEPTH {
        return Err("descriptors nested too deep".into());
    }
    Ok(match &kind {
        b"Objc" | b"GlbO" => descriptor_at(r, depth + 1)?,
        b"VlLs" => {
            let n = r.u32()?;
            let mut out = Vec::new();
            for _ in 0..n {
                let k = r.key()?;
                out.push(item(r, k, depth + 1)?);
            }
            Value::Array(out)
        }
        b"doub" => number(r.f64()?),
        b"UntF" => {
            let unit = String::from_utf8_lossy(&r.key()?).into_owned();
            json!({ "_unit": unit, "value": number(r.f64()?) })
        }
        b"UnFl" => unit_floats(r)?,
        b"TEXT" => Value::String(r.unicode()?),
        b"enum" => {
            let ty = id(r)?;
            let value = id(r)?;
            Value::String(format!("{ty}.{value}"))
        }
        b"long" => json!(r.i32()?),
        b"comp" => json!(r.i64()?),
        b"bool" => json!(r.u8()? != 0),
        b"type" | b"GlbC" => {
            let _name = r.unicode()?;
            Value::String(id(r)?)
        }
        b"alis" | b"tdta" | b"Pth " => {
            let n = r.u32()? as usize;
            let data = r.take(n)?;
            json!({ "_bytes": n, "_hash": format!("{:016x}", fingerprint(&[data])) })
        }
        b"obj " => {
            reference(r)?;
            Value::Null
        }
        b"ObAr" => object_array(r, depth)?,
        other => {
            return Err(format!(
                "an unknown descriptor item type {:?}",
                String::from_utf8_lossy(other)
            ))
        }
    })
}

fn number(x: f64) -> Value {
    serde_json::Number::from_f64(x).map_or(Value::Null, Value::Number)
}

/// Several numbers in one unit (a warp's mesh, an object array's columns).
fn unit_floats(r: &mut Reader) -> Result<Value> {
    let unit = String::from_utf8_lossy(&r.key()?).into_owned();
    let n = r.u32()? as usize;
    if n > r.left() / 8 {
        return Err("a unit list longer than the file".into());
    }
    let mut values = Vec::with_capacity(n);
    for _ in 0..n {
        values.push(number(r.f64()?));
    }
    Ok(json!({ "_unit": unit, "values": values }))
}

/// An object array: one descriptor-shaped header whose items are columns.
fn object_array(r: &mut Reader, depth: usize) -> Result<Value> {
    let _rows = r.u32()?;
    let _name = r.unicode()?;
    let class = id(r)?;
    let count = r.u32()?;
    let mut map = Map::new();
    map.insert("_class".into(), Value::String(class));
    for _ in 0..count {
        let key = id(r)?;
        let kind = r.key()?;
        let value = if &kind == b"UnFl" {
            unit_floats(r)?
        } else {
            item(r, kind, depth + 1)?
        };
        map.insert(key, value);
    }
    Ok(Value::Object(map))
}

/// A reference to something in the running app: skipped, nothing in a saved
/// layer's look depends on it.
fn reference(r: &mut Reader) -> Result<()> {
    let n = r.u32()?;
    for _ in 0..n {
        match &r.key()? {
            b"prop" => {
                r.unicode()?;
                id(r)?;
                id(r)?;
            }
            b"Clss" => {
                r.unicode()?;
                id(r)?;
            }
            b"Enmr" => {
                r.unicode()?;
                id(r)?;
                id(r)?;
                id(r)?;
            }
            b"rele" => {
                r.unicode()?;
                id(r)?;
                r.u32()?;
            }
            b"Idnt" | b"indx" => {
                r.u32()?;
            }
            b"name" => {
                r.unicode()?;
                id(r)?;
                r.unicode()?;
            }
            other => {
                return Err(format!(
                    "an unknown reference kind {:?}",
                    String::from_utf8_lossy(other)
                ))
            }
        }
    }
    Ok(())
}

/// The number under `key` (a plain number, or a unit value's), if any.
pub(crate) fn num(d: &Value, key: &str) -> Option<f64> {
    let v = d.get(key)?;
    v.as_f64()
        .or_else(|| v.get("value").and_then(Value::as_f64))
}

/// A colour descriptor (`"Clr "`) as straight `[r, g, b, a]` in 0–1.
pub(crate) fn colour(c: &Value) -> Option<[f64; 4]> {
    let n = |k: &str| num(c, k);
    let class = c.get("_class")?.as_str()?;
    let rgb = match class {
        "RGBC" => match (n("redFloat"), n("greenFloat"), n("blueFloat")) {
            (Some(r), Some(g), Some(b)) => [r, g, b],
            _ => [n("Rd  ")? / 255.0, n("Grn ")? / 255.0, n("Bl  ")? / 255.0],
        },
        "Grsc" => {
            let g = 1.0 - n("Gry ")? / 100.0;
            [g, g, g]
        }
        "HSBC" => hsb(n("H   ")?, n("Strt")? / 100.0, n("Brgh")? / 100.0),
        "CMYC" => {
            let k = 1.0 - n("Blck")? / 100.0;
            [
                (1.0 - n("Cyn ")? / 100.0) * k,
                (1.0 - n("Mgnt")? / 100.0) * k,
                (1.0 - n("Ylw ")? / 100.0) * k,
            ]
        }
        _ => return None,
    };
    let clamp = |x: f64| x.clamp(0.0, 1.0);
    Some([clamp(rgb[0]), clamp(rgb[1]), clamp(rgb[2]), 1.0])
}

fn hsb(h: f64, s: f64, v: f64) -> [f64; 3] {
    let h = (h.rem_euclid(360.0)) / 60.0;
    let c = v * s;
    let x = c * (1.0 - (h % 2.0 - 1.0).abs());
    let (r, g, b) = match h as u32 {
        0 => (c, x, 0.0),
        1 => (x, c, 0.0),
        2 => (0.0, c, x),
        3 => (0.0, x, c),
        4 => (x, 0.0, c),
        _ => (c, 0.0, x),
    };
    let m = v - c;
    [r + m, g + m, b + m]
}
