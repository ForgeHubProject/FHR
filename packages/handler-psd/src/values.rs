//! What one changed property reads as: a label and its before and after values,
//! in the terms Photoshop's panels show them.
//!
//! The renderer's formatter reads these back (renderer-sdk format.ts): a label
//! naming a colour with an `[r, g, b, a]` tuple becomes a colour chip, a
//! "Position" pair gets its move as a delta, an angle carries its `°`.

use fhr::{ChangeKind, DiffChange};
use serde_json::{json, Value};

use crate::descriptor::{colour, num as dnum};
use crate::file::{Document, Kind, Layer, Placed, Rect, Style, Text};

/// One changed property.
pub(crate) struct Leaf {
    pub label: String,
    pub before: Value,
    pub after: Value,
}

impl Leaf {
    pub(crate) fn new(
        label: impl Into<String>,
        before: impl Into<Value>,
        after: impl Into<Value>,
    ) -> Leaf {
        Leaf {
            label: label.into(),
            before: before.into(),
            after: after.into(),
        }
    }

    /// The row: under `parent`, at a path made from the label.
    pub(crate) fn change(self, parent: &str) -> DiffChange {
        DiffChange::new(
            format!("{parent}/{}", slug(&self.label)),
            ChangeKind::Modified,
            self.label,
        )
        .before(self.before)
        .after(self.after)
    }
}

/// `leaf` if its sides differ.
fn diff(out: &mut Vec<Leaf>, label: &str, before: impl Into<Value>, after: impl Into<Value>) {
    let (b, a) = (before.into(), after.into());
    if b != a {
        out.push(Leaf::new(label, b, a));
    }
}

pub(crate) fn document_leaves(b: &Document, h: &Document) -> Vec<Leaf> {
    let mut out = Vec::new();
    diff(
        &mut out,
        "Canvas size",
        format!("{} × {}", b.width, b.height),
        format!("{} × {}", h.width, h.height),
    );
    diff(&mut out, "Colour mode", mode(b.mode), mode(h.mode));
    diff(
        &mut out,
        "Bit depth",
        format!("{}-bit", b.depth),
        format!("{}-bit", h.depth),
    );
    let ppi = |d: &Document| {
        d.resolution
            .map_or(Value::Null, |r| json!(format!("{} ppi", text(&num(r)))))
    };
    diff(&mut out, "Resolution", ppi(b), ppi(h));
    let guides = |d: &Document| {
        if d.guides.is_empty() {
            json!("none")
        } else {
            json!(d.guides.join(", "))
        }
    };
    diff(&mut out, "Guides", guides(b), guides(h));
    out
}

fn mode(m: u16) -> &'static str {
    match m {
        0 => "Bitmap",
        1 => "Grayscale",
        2 => "Indexed",
        3 => "RGB",
        4 => "CMYK",
        7 => "Multichannel",
        8 => "Duotone",
        9 => "Lab",
        _ => "other",
    }
}

/// What a layer is, as the Layers panel would call it.
pub(crate) fn kind_label(k: &Kind) -> &'static str {
    match k {
        Kind::Pixel => "Layer",
        Kind::Text => "Text",
        Kind::Shape => "Shape",
        Kind::Fill => "Fill layer",
        Kind::Smart => "Smart object",
        Kind::Group => "Group",
        Kind::Adjustment(name) => name,
    }
}

/// `Text "Hello World"`.
pub(crate) fn layer_label(l: &Layer) -> String {
    format!("{} \"{}\"", kind_label(&l.kind), clip(&l.name, 80))
}

/// What an added or removed layer is, in a few words.
pub(crate) fn summary(l: &Layer, doc: &Document) -> Value {
    if let Some(t) = &l.text {
        return json!(clip(&t.content.replace('\n', " "), 80));
    }
    if l.kind == Kind::Group {
        let n = doc.layers.iter().filter(|c| c.parent == Some(l.id)).count();
        return json!(format!("{n} layer{}", if n == 1 { "" } else { "s" }));
    }
    let r = bounds(l);
    if r.is_empty() {
        return json!(kind_label(&l.kind));
    }
    json!(format!(
        "{} × {}",
        text(&num(r.width())),
        text(&num(r.height()))
    ))
}

/// Where a layer is drawn on the canvas: its pixels, or its vector shape.
pub(crate) fn bounds(l: &Layer) -> Rect {
    match (&l.kind, &l.vector) {
        (Kind::Shape | Kind::Fill, Some(v)) => v.rect,
        _ => l.rect,
    }
}

/// The rows for a layer that is on both sides. `moved` says whether its
/// place in the stack changed; `bd`/`hd` find group names.
pub(crate) fn layer_leaves(
    bd: &Document,
    b: &Layer,
    hd: &Document,
    h: &Layer,
    moved: bool,
) -> Vec<Leaf> {
    let mut out = Vec::new();
    if b.kind != h.kind {
        out.push(Leaf::new("Kind", kind_label(&b.kind), kind_label(&h.kind)));
    }
    // A text layer is named after its text until someone renames it: then the
    // name says nothing the text doesn't.
    let follows = |l: &Layer| {
        l.text.as_ref().is_some_and(|t| {
            t.content
                .replace('\n', " ")
                .starts_with(l.name.trim_end_matches('…'))
        })
    };
    if !(follows(b) && follows(h)) {
        diff(&mut out, "Name", b.name.as_str(), h.name.as_str());
    }
    if let (Some(bt), Some(ht)) = (&b.text, &h.text) {
        diff(&mut out, "Text", bt.content.as_str(), ht.content.as_str());
    }
    diff(&mut out, "Visibility", shown(b.visible), shown(h.visible));
    diff(&mut out, "Opacity", percent(b.opacity), percent(h.opacity));
    diff(
        &mut out,
        "Fill opacity",
        percent(b.fill_opacity),
        percent(h.fill_opacity),
    );
    diff(&mut out, "Blend mode", blend(&b.blend), blend(&h.blend));
    diff(
        &mut out,
        "Clipping mask",
        clipped(b.clipped),
        clipped(h.clipped),
    );
    diff(&mut out, "Locks", locks(b.locks), locks(h.locks));
    diff(&mut out, "Colour label", label(b.label), label(h.label));
    let group = |d: &Document, l: &Layer| -> Value {
        l.parent
            .and_then(|p| d.layers.iter().find(|g| g.id == p))
            .map_or(json!("—"), |g| json!(g.name))
    };
    diff(&mut out, "Group", group(bd, b), group(hd, h));
    if moved {
        let place = |d: &Document, l: &Layer| {
            let i = d.layers.iter().position(|x| x.id == l.id).unwrap_or(0) + 1;
            format!("{i} of {} from the top", d.layers.len())
        };
        out.push(Leaf::new("Stack order", place(bd, b), place(hd, h)));
    }

    match (&b.text, &h.text, &b.placed, &h.placed) {
        (Some(bt), Some(ht), _, _) => text_leaves(&mut out, bt, ht),
        (_, _, Some(bp), Some(hp)) => {
            placed_leaves(&mut out, bp, hp);
            let content = |p: &Placed, d: &Document| {
                p.source
                    .as_ref()
                    .and_then(|s| d.embedded.get(s))
                    .map(|f| format!("{f:016x}"))
            };
            if content(bp, bd) != content(hp, hd) || bp.source != hp.source {
                out.push(Leaf::new("Contents", "before", "edited"));
            }
        }
        _ => {
            let (br, hr) = (bounds(b), bounds(h));
            if !(br.is_empty() && hr.is_empty()) {
                diff(
                    &mut out,
                    "Position",
                    point(br.left, br.top),
                    point(hr.left, hr.top),
                );
                diff(&mut out, "Width", num(br.width()), num(hr.width()));
                diff(&mut out, "Height", num(br.height()), num(hr.height()));
            }
        }
    }
    if b.kind == Kind::Pixel && h.kind == Kind::Pixel && b.pixels != h.pixels {
        out.push(Leaf::new("Pixels", "before", "edited"));
    }
    if let (Some(bs), Some(hs)) = (b.settings, h.settings) {
        if bs != hs {
            out.push(Leaf::new("Settings", "before", "edited"));
        }
    }
    fill_leaves(&mut out, b.fill.as_ref(), h.fill.as_ref());
    stroke_leaves(&mut out, b.stroke.as_ref(), h.stroke.as_ref());
    mask_leaves(&mut out, b, h);
    effect_leaves(&mut out, b.effects.as_ref(), h.effects.as_ref());
    out
}

fn text_leaves(out: &mut Vec<Leaf>, b: &Text, h: &Text) {
    let [_, _, _, _, btx, bty] = b.transform;
    let [_, _, _, _, htx, hty] = h.transform;
    diff(out, "Position", point(btx, bty), point(htx, hty));
    diff(
        out,
        "Rotation",
        degrees(turn(b.transform)),
        degrees(turn(h.transform)),
    );
    // The size the Character panel shows: the font size as the text is scaled.
    let (bs, hs) = (scale(b.transform), scale(h.transform));
    let runs = b.runs.len().max(h.runs.len());
    let one = |s: &[Style], f: &dyn Fn(&Style) -> Value| -> Option<Value> {
        let first = f(s.first()?);
        s.iter().all(|x| f(x) == first).then_some(first)
    };
    let props: [(&str, &StyleValue); 8] = [
        ("Font", &|s, _| {
            s.font.as_deref().map_or(Value::Null, |f| json!(f))
        }),
        ("Font size", &|s, k| {
            s.size.map_or(Value::Null, |x| num(x * k))
        }),
        ("Colour", &|s, _| {
            s.colour.map_or(Value::Null, |c| json!(c.map(round3)))
        }),
        ("Tracking", &|s, _| s.tracking.map_or(Value::Null, num)),
        ("Leading", &|s, k| {
            s.leading.map_or(json!("auto"), |x| num(x * k))
        }),
        ("Faux bold", &|s, _| json!(yes_no(s.bold))),
        ("Faux italic", &|s, _| json!(yes_no(s.italic))),
        ("Underline", &|s, _| json!(yes_no(s.underline))),
    ];
    for (label, get) in props {
        let (bv, hv) = (one(&b.runs, &|s| get(s, bs)), one(&h.runs, &|s| get(s, hs)));
        match (bv, hv) {
            (Some(x), Some(y)) => diff(out, label, x, y),
            // Styled in parts: say which part changed.
            _ => {
                for i in 0..runs {
                    let at = |s: &[Style], k| s.get(i).map_or(Value::Null, |x| get(x, k));
                    diff(
                        out,
                        &format!("{label} (part {})", i + 1),
                        at(&b.runs, bs),
                        at(&h.runs, hs),
                    );
                }
            }
        }
    }
    diff(
        out,
        "Alignment",
        b.align.unwrap_or("left"),
        h.align.unwrap_or("left"),
    );
    let warp = |t: &Text| {
        t.warp.as_deref().map_or(json!("none"), |w| {
            json!(humanize(w.trim_start_matches("warp")))
        })
    };
    diff(out, "Warp", warp(b), warp(h));
}

/// One styling property of a run, at the text's scale.
type StyleValue = dyn Fn(&Style, f64) -> Value;

fn turn(t: [f64; 6]) -> f64 {
    t[1].atan2(t[0]).to_degrees()
}

fn scale(t: [f64; 6]) -> f64 {
    t[2].hypot(t[3])
}

/// A placed object's corners as Free Transform reads them: its centre, its
/// own width and height, the angle it's turned by, and whether it's flipped.
fn placed_leaves(out: &mut Vec<Leaf>, b: &Placed, h: &Placed) {
    let read = |q: [f64; 8]| {
        let [x0, y0, x1, y1, x2, y2, x3, y3] = q;
        let centre = ((x0 + x1 + x2 + x3) / 4.0, (y0 + y1 + y2 + y3) / 4.0);
        let width = (x1 - x0).hypot(y1 - y0);
        let height = (x3 - x0).hypot(y3 - y0);
        let angle = (y1 - y0).atan2(x1 - x0).to_degrees();
        let flipped = (x1 - x0) * (y3 - y0) - (y1 - y0) * (x3 - x0) < 0.0;
        (centre, width, height, angle, flipped)
    };
    let (bc, bw, bh, ba, bf) = read(b.quad);
    let (hc, hw, hh, ha, hf) = read(h.quad);
    diff(
        out,
        "Position (centre)",
        point(bc.0, bc.1),
        point(hc.0, hc.1),
    );
    diff(out, "Width", num(bw), num(hw));
    diff(out, "Height", num(bh), num(hh));
    diff(out, "Rotation", degrees(ba), degrees(ha));
    diff(
        out,
        "Flip",
        if bf { "flipped" } else { "not flipped" },
        if hf { "flipped" } else { "not flipped" },
    );
}

/// A shape's or fill layer's content: a solid colour as a chip, anything
/// else (a gradient, a pattern) as edited.
fn fill_leaves(out: &mut Vec<Leaf>, b: Option<&Value>, h: Option<&Value>) {
    let what = |v: Option<&Value>| -> Value {
        match v {
            None => Value::Null,
            Some(d) if d.get("Clr ").is_some() => json!("solid colour"),
            Some(d) if d.get("Grad").is_some() => json!("gradient"),
            Some(d) if d.get("Ptrn").is_some() => json!("pattern"),
            Some(_) => json!("fill"),
        }
    };
    let (bw, hw) = (what(b), what(h));
    if bw != hw {
        out.push(Leaf::new("Fill", bw, hw));
        return;
    }
    let (Some(b), Some(h)) = (b, h) else { return };
    let chip = |d: &Value| d.get("Clr ").and_then(colour).map(|c| json!(c.map(round3)));
    match (chip(b), chip(h)) {
        (Some(x), Some(y)) => diff(out, "Fill colour", x, y),
        _ if b != h => out.push(Leaf::new(
            format!("Fill ({})", text(&hw)),
            "before",
            "edited",
        )),
        _ => {}
    }
}

fn stroke_leaves(out: &mut Vec<Leaf>, b: Option<&Value>, h: Option<&Value>) {
    let (Some(b), Some(h)) = (b, h) else { return };
    let on = |d: &Value, k: &str| {
        d.get(k)
            .and_then(Value::as_bool)
            .map_or(Value::Null, |x| json!(if x { "on" } else { "off" }))
    };
    diff(
        out,
        "Stroke",
        on(b, "strokeEnabled"),
        on(h, "strokeEnabled"),
    );
    diff(out, "Fill", on(b, "fillEnabled"), on(h, "fillEnabled"));
    let width = |d: &Value| dnum(d, "strokeStyleLineWidth").map_or(Value::Null, num);
    diff(out, "Stroke width", width(b), width(h));
    let chip = |d: &Value| {
        d.pointer("/strokeStyleContent/Clr ")
            .and_then(colour)
            .map_or(Value::Null, |c| json!(c.map(round3)))
    };
    diff(out, "Stroke colour", chip(b), chip(h));
    let opacity = |d: &Value| {
        dnum(d, "strokeStyleOpacity").map_or(Value::Null, |x| json!(format!("{}%", text(&num(x)))))
    };
    diff(out, "Stroke opacity", opacity(b), opacity(h));
    let align = |d: &Value| {
        d.get("strokeStyleLineAlignment")
            .and_then(Value::as_str)
            .map_or(Value::Null, |a| {
                json!(humanize(
                    a.rsplit('.')
                        .next()
                        .unwrap_or(a)
                        .trim_start_matches("strokeStyleAlign")
                ))
            })
    };
    diff(out, "Stroke position", align(b), align(h));
}

fn mask_leaves(out: &mut Vec<Leaf>, b: &Layer, h: &Layer) {
    match (&b.mask, &h.mask) {
        (None, Some(_)) => out.push(Leaf::new("Layer mask", "none", "added")),
        (Some(_), None) => out.push(Leaf::new("Layer mask", "yes", "removed")),
        (Some(bm), Some(hm)) => {
            diff(
                out,
                "Layer mask",
                on_off(!bm.disabled),
                on_off(!hm.disabled),
            );
            // Moved with its layer is no edit of its own: its pixels say.
            if bm.pixels != hm.pixels {
                out.push(Leaf::new("Layer mask pixels", "before", "edited"));
            }
        }
        (None, None) => {}
    }
    if b.kind == Kind::Shape && h.kind == Kind::Shape {
        if let (Some(bv), Some(hv)) = (&b.vector, &h.vector) {
            if bv.path != hv.path && bv.rect == hv.rect {
                out.push(Leaf::new("Shape", "before", "edited"));
            }
        }
        return;
    }
    match (&b.vector, &h.vector) {
        (None, Some(_)) => out.push(Leaf::new("Vector mask", "none", "added")),
        (Some(_), None) => out.push(Leaf::new("Vector mask", "yes", "removed")),
        (Some(bv), Some(hv)) => {
            diff(
                out,
                "Vector mask",
                on_off(!bv.disabled),
                on_off(!hv.disabled),
            );
            if bv.path != hv.path {
                out.push(Leaf::new("Vector mask path", "before", "edited"));
            }
        }
        (None, None) => {}
    }
}

/// Layer effects, one by one: on or off, and the settings the Layer Style
/// dialog leads with.
fn effect_leaves(out: &mut Vec<Leaf>, b: Option<&Value>, h: Option<&Value>) {
    let master = |d: Option<&Value>| {
        d.and_then(|d| d.get("masterFXSwitch"))
            .and_then(Value::as_bool)
            .map_or(Value::Null, |x| json!(if x { "on" } else { "off" }))
    };
    if b.is_some() && h.is_some() {
        diff(out, "Effects", master(b), master(h));
    }
    let (be, he) = (effects(b), effects(h));
    let mut names: Vec<&String> = be.iter().chain(he.iter()).map(|(n, _)| n).collect();
    names.dedup();
    let mut seen = std::collections::BTreeSet::new();
    for name in names {
        if !seen.insert(name.clone()) {
            continue;
        }
        fn find<'v>(list: &[(String, &'v Value)], name: &str) -> Option<&'v Value> {
            list.iter().find(|(n, _)| n == name).map(|(_, v)| *v)
        }
        let on = |d: &Value| d.get("enab").and_then(Value::as_bool).unwrap_or(true);
        match (find(&be, name), find(&he, name)) {
            // A new effect: on, and the settings it was given.
            (None, Some(e)) => {
                out.push(Leaf::new(name.clone(), "none", on_off(on(e))));
                effect_settings(out, name, None, Some(e));
            }
            (Some(e), None) => out.push(Leaf::new(name.clone(), on_off(on(e)), "removed")),
            (Some(x), Some(y)) if x != y => {
                let before = out.len();
                diff(out, name, on_off(on(x)), on_off(on(y)));
                effect_settings(out, name, Some(x), Some(y));
                if out.len() == before {
                    out.push(Leaf::new(format!("{name} settings"), "before", "edited"));
                }
            }
            _ => {}
        }
    }
}

/// The settings the Layer Style dialog leads with, where they differ: colour,
/// opacity, blend mode, size, distance, spread, angle. A side without the
/// effect reads as none.
fn effect_settings(out: &mut Vec<Leaf>, name: &str, x: Option<&Value>, y: Option<&Value>) {
    let side = |d: Option<&Value>, f: &dyn Fn(&Value) -> Option<Value>| {
        d.and_then(f).unwrap_or(Value::Null)
    };
    let chip = |d: &Value| d.get("Clr ").and_then(colour).map(|c| json!(c.map(round3)));
    diff(
        out,
        &format!("{name} colour"),
        side(x, &chip),
        side(y, &chip),
    );
    let pct = |d: &Value| dnum(d, "Opct").map(|v| json!(format!("{}%", text(&num(v)))));
    diff(
        out,
        &format!("{name} opacity"),
        side(x, &pct),
        side(y, &pct),
    );
    let mode = |d: &Value| {
        d.get("Md  ")
            .and_then(Value::as_str)
            .map(|m| json!(effect_blend(m)))
    };
    diff(
        out,
        &format!("{name} blend mode"),
        side(x, &mode),
        side(y, &mode),
    );
    for (key, what) in [
        ("blur", "size"),
        ("Sz  ", "size"),
        ("Dstn", "distance"),
        ("Ckmt", "spread"),
    ] {
        let v = |d: &Value| dnum(d, key).map(num);
        diff(out, &format!("{name} {what}"), side(x, &v), side(y, &v));
    }
    let angle = |d: &Value| dnum(d, "lagl").map(degrees);
    diff(
        out,
        &format!("{name} angle"),
        side(x, &angle),
        side(y, &angle),
    );
}

/// Each effect in a layer style, named the way the Layer Style dialog names
/// it ("Drop Shadow", "Drop Shadow 2" for a second one).
fn effects(d: Option<&Value>) -> Vec<(String, &Value)> {
    const NAMES: &[(&str, &str, &str)] = &[
        ("DrSh", "dropShadowMulti", "Drop Shadow"),
        ("IrSh", "innerShadowMulti", "Inner Shadow"),
        ("OrGl", "", "Outer Glow"),
        ("IrGl", "", "Inner Glow"),
        ("ebbl", "", "Bevel & Emboss"),
        ("ChFX", "", "Satin"),
        ("SoFi", "solidFillMulti", "Color Overlay"),
        ("GrFl", "gradientFillMulti", "Gradient Overlay"),
        ("patternFill", "", "Pattern Overlay"),
        ("FrFX", "frameFXMulti", "Stroke"),
    ];
    let mut out = Vec::new();
    let Some(d) = d else { return out };
    for (one, many, name) in NAMES {
        let list: Vec<&Value> = match d.get(*many).and_then(Value::as_array) {
            Some(a) if !many.is_empty() => a.iter().collect(),
            _ => d.get(*one).into_iter().collect(),
        };
        for (i, e) in list.into_iter().enumerate() {
            let label = if i == 0 {
                (*name).to_string()
            } else {
                format!("{name} {}", i + 1)
            };
            out.push((label, e));
        }
    }
    out
}

fn effect_blend(m: &str) -> String {
    let code = m.rsplit('.').next().unwrap_or(m);
    match code {
        "Nrml" => "Normal".into(),
        "Dslv" => "Dissolve".into(),
        "Drkn" => "Darken".into(),
        "Mltp" => "Multiply".into(),
        "CBrn" => "Color Burn".into(),
        "Lghn" => "Lighten".into(),
        "Scrn" => "Screen".into(),
        "CDdg" => "Color Dodge".into(),
        "Ovrl" => "Overlay".into(),
        "SftL" => "Soft Light".into(),
        "HrdL" => "Hard Light".into(),
        "Dfrn" => "Difference".into(),
        "Xclu" => "Exclusion".into(),
        "H   " => "Hue".into(),
        "Strt" => "Saturation".into(),
        "Clr " => "Color".into(),
        "Lmns" => "Luminosity".into(),
        other => humanize(other),
    }
}

/// A layer's blend mode, as the Layers panel names it.
pub(crate) fn blend(key: &[u8; 4]) -> String {
    match key {
        b"pass" => "Pass Through",
        b"norm" => "Normal",
        b"diss" => "Dissolve",
        b"dark" => "Darken",
        b"mul " => "Multiply",
        b"idiv" => "Color Burn",
        b"lbrn" => "Linear Burn",
        b"dkCl" => "Darker Color",
        b"lite" => "Lighten",
        b"scrn" => "Screen",
        b"div " => "Color Dodge",
        b"lddg" => "Linear Dodge (Add)",
        b"lgCl" => "Lighter Color",
        b"over" => "Overlay",
        b"sLit" => "Soft Light",
        b"hLit" => "Hard Light",
        b"vLit" => "Vivid Light",
        b"lLit" => "Linear Light",
        b"pLit" => "Pin Light",
        b"hMix" => "Hard Mix",
        b"diff" => "Difference",
        b"smud" => "Exclusion",
        b"fsub" => "Subtract",
        b"fdiv" => "Divide",
        b"hue " => "Hue",
        b"sat " => "Saturation",
        b"colr" => "Color",
        b"lum " => "Luminosity",
        other => return String::from_utf8_lossy(other).trim().to_string(),
    }
    .into()
}

fn locks(flags: u32) -> String {
    if flags & 0x8000_0000 != 0 {
        return "all".into();
    }
    let names: Vec<&str> = [(1, "transparency"), (2, "pixels"), (4, "position")]
        .iter()
        .filter(|(bit, _)| flags & bit != 0)
        .map(|(_, n)| *n)
        .collect();
    if names.is_empty() {
        "none".into()
    } else {
        names.join(", ")
    }
}

fn label(l: u16) -> &'static str {
    match l {
        0 => "none",
        1 => "red",
        2 => "orange",
        3 => "yellow",
        4 => "green",
        5 => "blue",
        6 => "violet",
        7 => "grey",
        _ => "other",
    }
}

pub(crate) fn file_data_changed() -> DiffChange {
    DiffChange::new("file", ChangeKind::Modified, "File data")
        .after("changed outside the layers (thumbnail, metadata or saved state)")
}

fn shown(v: bool) -> &'static str {
    if v {
        "visible"
    } else {
        "hidden"
    }
}

fn clipped(v: bool) -> &'static str {
    if v {
        "clipped"
    } else {
        "not clipped"
    }
}

fn on_off(v: bool) -> &'static str {
    if v {
        "on"
    } else {
        "off"
    }
}

fn yes_no(v: bool) -> &'static str {
    if v {
        "yes"
    } else {
        "no"
    }
}

/// 0–255 as the whole percent Photoshop's panels show.
fn percent(x: u8) -> String {
    format!("{}%", (f64::from(x) / 255.0 * 100.0).round())
}

/// An angle in (-180°, 180°], as text with its `°`.
fn degrees(a: f64) -> Value {
    let mut a = a.rem_euclid(360.0);
    if a > 180.0 {
        a -= 360.0;
    }
    json!(format!("{}°", text(&num(a))))
}

fn point(x: f64, y: f64) -> Value {
    json!([num(x), num(y)])
}

pub(crate) fn num(x: f64) -> Value {
    if !x.is_finite() {
        return Value::Null;
    }
    let r = (x * 100.0).round() / 100.0;
    let r = if r == 0.0 { 0.0 } else { r };
    if r == r.trunc() && r.abs() < 1e15 {
        json!(r as i64)
    } else {
        json!(r)
    }
}

fn round3(x: f64) -> f64 {
    (x * 1000.0).round() / 1000.0
}

/// A value's text, for building other text from it.
fn text(v: &Value) -> String {
    match v {
        Value::String(s) => s.clone(),
        other => other.to_string(),
    }
}

/// `"strokeStyleAlignInside"` → "Inside", `"ArcLower"` → "Arc lower".
fn humanize(s: &str) -> String {
    let mut out = String::new();
    for (i, c) in s.chars().enumerate() {
        if i > 0 && c.is_uppercase() {
            out.push(' ');
            out.extend(c.to_lowercase());
        } else if i == 0 {
            out.extend(c.to_uppercase());
        } else {
            out.push(c);
        }
    }
    out
}

/// `s` cut to `max` characters.
pub(crate) fn clip(s: &str, max: usize) -> String {
    if s.chars().count() <= max {
        return s.to_string();
    }
    let mut out: String = s.chars().take(max).collect();
    out.push('…');
    out
}

pub(crate) fn slug(label: &str) -> String {
    let mut out = String::new();
    for c in label.chars() {
        if c.is_alphanumeric() {
            out.extend(c.to_lowercase());
        } else if !out.ends_with('-') && !out.is_empty() {
            out.push('-');
        }
    }
    out.trim_end_matches('-').to_string()
}
