//! What one changed property reads as: a label and its before and after values,
//! in the terms the design panel shows them.
//!
//! The renderer's formatter reads these back (renderer-sdk format.ts): a label
//! naming a colour with an `[r, g, b, a]` tuple becomes a colour chip, a
//! "Position" pair gets its move as a delta, an angle carries its `°`. Anything
//! without a rule here falls back to its JSON, shortened.

use fhr::{ChangeKind, DiffChange};
use figcraft_collab::Prop;
use figcraft_doc::color::{Color, Paint};
use figcraft_doc::geom::{is_flipped, rotation_degrees};
use figcraft_doc::{Document, Node, NodeId, NodeKind, Sizing, TextAutoResize};
use serde_json::{json, Map, Value};

/// One changed property of one layer (or page, or document entry).
pub(crate) struct Leaf {
    pub label: String,
    pub before: Value,
    pub after: Value,
    /// Moved or resized by layout (auto layout, hugging text, a group fitting
    /// its layers) rather than by someone editing this property.
    pub by_layout: bool,
}

impl Leaf {
    fn new(label: impl Into<String>, before: impl Into<Value>, after: impl Into<Value>) -> Leaf {
        let (before, mut after) = (before.into(), after.into());
        // Two values that read the same but differ underneath: say so, or the
        // row reads as no change at all.
        if before == after {
            if let Value::String(s) = &mut after {
                s.push_str(" (edited)");
            } else {
                after = Value::String(format!("{} (edited)", text(&after)));
            }
        }
        Leaf {
            label: label.into(),
            before,
            after,
            by_layout: false,
        }
    }

    fn by_layout(mut self, yes: bool) -> Leaf {
        self.by_layout = yes;
        self
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

/// The rows for property `prop` of a layer that is on both sides (`b` in
/// `base`, `h` in `head`). `before`/`after` are the property's JSON, for the
/// properties without a rule of their own.
pub(crate) fn node_leaves(
    prop: Prop,
    (base, b): (&Document, &Node),
    (head, h): (&Document, &Node),
    before: Option<&Value>,
    after: &Value,
) -> Vec<Leaf> {
    let one = |l: Leaf| vec![l];
    match prop {
        Prop::Name => one(Leaf::new("Name", b.name.as_str(), h.name.as_str())),
        Prop::Kind => kind_leaves((base, &b.kind), (head, &h.kind)),
        Prop::Visible => one(Leaf::new("Visibility", shown(b.visible), shown(h.visible))),
        Prop::Locked => one(Leaf::new("Lock", locked(b.locked), locked(h.locked))),
        Prop::Opacity => changed(Leaf::new("Opacity", percent(b.opacity), percent(h.opacity))),
        Prop::Transform => transform_leaves(head, b, h),
        Prop::Width => changed(
            Leaf::new("Width", num(b.width), num(h.width)).by_layout(sized_by_layout(h, true)),
        ),
        Prop::Height => changed(
            Leaf::new("Height", num(b.height), num(h.height)).by_layout(sized_by_layout(h, false)),
        ),
        Prop::Fills => paint_leaves("Fill", &b.fills, &h.fills),
        Prop::Strokes => paint_leaves("Stroke", &b.strokes, &h.strokes),
        Prop::Corners => corner_leaves(b, h),
        Prop::Clip => one(Leaf::new("Clip content", yes_no(b.clip), yes_no(h.clip))),
        Prop::Layout => match (&b.layout, &h.layout) {
            (None, Some(_)) | (Some(_), None) => one(Leaf::new(
                "Auto layout",
                layout_text(before),
                layout_text(Some(after)),
            )),
            _ => generic("Auto layout", before, Some(after)),
        },
        Prop::ChildLayout => generic("Resizing", before, Some(after)),
        // FigCraft notes an override on any edited layer; it means something
        // only inside a component instance.
        Prop::Overrides if !in_instance(head, h) => Vec::new(),
        Prop::Parent => one(Leaf::new(
            "Parent",
            b.parent
                .map_or_else(|| "none".into(), |p| node_label(base, p)),
            h.parent
                .map_or_else(|| "none".into(), |p| node_label(head, p)),
        )),
        Prop::Position => {
            let what = if h.parent.is_none() {
                "Page order"
            } else {
                "Layer order"
            };
            one(Leaf::new(what, order(base, b), order(head, h)))
        }
        _ => generic(&prop_label(prop), before, Some(after)),
    }
    .into_iter()
    .filter(|l| l.before != l.after)
    .collect()
}

/// A leaf whose values may round to the same display: dropped then (a layout
/// pass that moved something by 0.0001 px moved nothing anyone can see).
fn changed(l: Leaf) -> Vec<Leaf> {
    if l.after.as_str().is_some_and(|s| s.ends_with(" (edited)")) {
        Vec::new()
    } else {
        vec![l]
    }
}

/// `Text "Title"`: what a layer is and its name, the way the layers panel
/// would point at it.
pub(crate) fn node_label(doc: &Document, id: NodeId) -> String {
    doc.node(id).map_or_else(
        || format!("Layer {id}"),
        |n| format!("{} \"{}\"", n.kind.label(), n.name),
    )
}

/// What a layer is, for its Added or Removed row: a text's words, otherwise
/// its size and how many layers it holds.
pub(crate) fn summary(doc: &Document, id: NodeId) -> String {
    let Some(n) = doc.node(id) else {
        return String::new();
    };
    if let NodeKind::Text(t) = &n.kind {
        return clip(&t.characters, 120);
    }
    let inside = doc.subtree(id).len().saturating_sub(1);
    let layers = match inside {
        0 => String::new(),
        1 => ", 1 layer inside".into(),
        k => format!(", {k} layers inside"),
    };
    if n.is_page() {
        return match inside {
            0 => "empty".into(),
            1 => "1 layer".into(),
            k => format!("{k} layers"),
        };
    }
    format!("{} × {}{layers}", text(&num(n.width)), text(&num(n.height)))
}

/// The row for a file whose bytes changed with no change to the design.
pub(crate) fn file_data_changed() -> DiffChange {
    DiffChange::new("file", ChangeKind::Modified, "File data")
        .after("changed outside the design (thumbnail or saved state)")
}

fn kind_leaves((base, b): (&Document, &NodeKind), (head, h): (&Document, &NodeKind)) -> Vec<Leaf> {
    if b.type_name() != h.type_name() {
        return vec![Leaf::new("Type", b.label(), h.label())];
    }
    let (Ok(bj), Ok(hj)) = (serde_json::to_value(b), serde_json::to_value(h)) else {
        return Vec::new();
    };
    let (Some(bo), Some(ho)) = (bj.as_object(), hj.as_object()) else {
        return Vec::new();
    };
    let mut out = Vec::new();
    for key in keys(bo, ho) {
        let (bv, hv) = (bo.get(key), ho.get(key));
        if bv == hv || key == "type" {
            continue;
        }
        match (h, key) {
            (NodeKind::Text(_), "characters") => out.push(Leaf::new(
                "Text",
                clip(bv.and_then(Value::as_str).unwrap_or_default(), 500),
                clip(hv.and_then(Value::as_str).unwrap_or_default(), 500),
            )),
            (NodeKind::Text(_), "style") => out.extend(text_style_leaves(bv, hv)),
            (NodeKind::Text(_), "runs") => out.push(Leaf::new(
                "Styled ranges",
                count(bv, "range", "ranges"),
                count(hv, "range", "ranges"),
            )),
            (NodeKind::Page { .. }, "background") => out.push(Leaf::new(
                "Background colour",
                color_value(bv),
                color_value(hv),
            )),
            (NodeKind::Instance { .. }, "component") => {
                let label = |doc: &Document, v: Option<&Value>| {
                    v.and_then(Value::as_u64)
                        .map_or_else(|| "none".into(), |id| node_label(doc, NodeId(id)))
                };
                out.push(Leaf::new(
                    "Main component",
                    label(base, bv),
                    label(head, hv),
                ));
            }
            (NodeKind::Instance { .. } | NodeKind::Component { .. }, "props") => {
                out.extend(generic("Property", bv, hv));
            }
            (NodeKind::Vector { .. }, "network") => out.push(Leaf::new(
                "Path",
                count(bv.and_then(|n| n.get("vertices")), "point", "points"),
                count(hv.and_then(|n| n.get("vertices")), "point", "points"),
            )),
            _ => out.extend(generic(&humanize(key), bv, hv)),
        }
    }
    out
}

fn text_style_leaves(b: Option<&Value>, h: Option<&Value>) -> Vec<Leaf> {
    let empty = Map::new();
    let bo = b.and_then(Value::as_object).unwrap_or(&empty);
    let ho = h.and_then(Value::as_object).unwrap_or(&empty);
    let mut out = Vec::new();
    for key in keys(bo, ho) {
        let (bv, hv) = (bo.get(key), ho.get(key));
        if bv == hv {
            continue;
        }
        let label = match key {
            "font_family" => "Font",
            "font_style" => "Font style",
            "fills" => {
                out.push(Leaf::new("Text colour", paints_text(bv), paints_text(hv)));
                continue;
            }
            "style_id" => "Text style",
            other => {
                out.extend(generic(&humanize(other), bv, hv));
                continue;
            }
        };
        out.push(Leaf::new(label, display(bv), display(hv)));
    }
    out
}

fn transform_leaves(head: &Document, b: &Node, h: &Node) -> Vec<Leaf> {
    let (bc, hc) = (b.transform.as_coeffs(), h.transform.as_coeffs());
    let mut out = Vec::new();
    let (bp, hp) = (
        json!([num(bc[4]), num(bc[5])]),
        json!([num(hc[4]), num(hc[5])]),
    );
    if bp != hp {
        out.push(Leaf::new("Position", bp, hp).by_layout(moved_by_layout(head, h)));
    }
    let (br, hr) = (
        num(rotation_degrees(b.transform)),
        num(rotation_degrees(h.transform)),
    );
    if br != hr {
        out.push(Leaf::new(
            "Rotation",
            format!("{}°", text(&br)),
            format!("{}°", text(&hr)),
        ));
    }
    if is_flipped(b.transform) != is_flipped(h.transform) {
        out.push(Leaf::new(
            "Flip",
            if is_flipped(b.transform) {
                "flipped"
            } else {
                "not flipped"
            },
            if is_flipped(h.transform) {
                "flipped"
            } else {
                "not flipped"
            },
        ));
    }
    out
}

/// Fills or strokes. Lists of the same length compare paint by paint, a
/// solid colour as a colour; otherwise the whole list is one row.
fn paint_leaves(what: &str, b: &[Paint], h: &[Paint]) -> Vec<Leaf> {
    if b.len() != h.len() {
        return vec![Leaf::new(format!("{what}s"), paint_list(b), paint_list(h))];
    }
    let mut out = Vec::new();
    for (i, (pb, ph)) in b.iter().zip(h).enumerate() {
        if pb == ph {
            continue;
        }
        let name = if h.len() > 1 {
            format!("{what} {}", i + 1)
        } else {
            what.to_string()
        };
        match (pb.solid_color(), ph.solid_color()) {
            (Some(cb), Some(ch)) if pb.visible == ph.visible && pb.blend == ph.blend => {
                out.push(Leaf::new(format!("{name} colour"), rgba(cb), rgba(ch)));
            }
            _ => out.push(Leaf::new(name, paint_text(pb), paint_text(ph))),
        }
    }
    out
}

fn corner_leaves(b: &Node, h: &Node) -> Vec<Leaf> {
    let (b, h) = (b.corners, h.corners);
    let mut out = Vec::new();
    if b.radii != h.radii {
        out.push(match (b.uniform(), h.uniform()) {
            (Some(x), Some(y)) => Leaf::new("Corner radius", num(x), num(y)),
            _ => Leaf::new(
                "Corner radii",
                json!(b.radii.map(num)),
                json!(h.radii.map(num)),
            ),
        });
    }
    if b.smoothing != h.smoothing {
        out.push(Leaf::new(
            "Corner smoothing",
            percent(b.smoothing as f32),
            percent(h.smoothing as f32),
        ));
    }
    out.into_iter().filter(|l| l.before != l.after).collect()
}

/// The layer is an instance or inside one.
fn in_instance(doc: &Document, n: &Node) -> bool {
    std::iter::once(n.id).chain(doc.ancestors(n.id)).any(|id| {
        doc.node(id)
            .is_some_and(|a| matches!(a.kind, NodeKind::Instance { .. }))
    })
}

/// Auto layout positions this layer: its parent lays its layers out, and it
/// isn't placed absolutely.
fn moved_by_layout(head: &Document, n: &Node) -> bool {
    !n.child_layout.absolute
        && n.parent
            .and_then(|p| head.node(p))
            .is_some_and(|p| p.layout.is_some())
}

/// Something other than a resize sets this layer's size on an axis: it hugs
/// its contents or fills its auto layout parent, it is text that grows with
/// its words, or it is a group, whose bounds are its layers'.
fn sized_by_layout(n: &Node, horizontal: bool) -> bool {
    let sizing = if horizontal {
        n.child_layout.sizing_h
    } else {
        n.child_layout.sizing_v
    };
    if matches!(sizing, Sizing::Hug | Sizing::Fill) {
        return true;
    }
    match &n.kind {
        NodeKind::Text(t) => match t.auto_resize {
            TextAutoResize::WidthAndHeight => true,
            TextAutoResize::Height => !horizontal,
            _ => false,
        },
        NodeKind::Group | NodeKind::BooleanOp { .. } => true,
        _ => false,
    }
}

/// Layers are listed top first, the way the layers panel lists them (the last
/// child paints on top); pages in their own order.
fn order(doc: &Document, n: &Node) -> String {
    let siblings = match n.parent.and_then(|p| doc.node(p)) {
        Some(p) => p.children.iter().rev().copied().collect::<Vec<_>>(),
        None => doc.pages.clone(),
    };
    match siblings.iter().position(|s| *s == n.id) {
        Some(i) => format!("{} of {}", i + 1, siblings.len()),
        None => "none".into(),
    }
}

/// The rows for a property without a rule of its own: per field for an
/// object, else one row.
pub(crate) fn generic(label: &str, b: Option<&Value>, h: Option<&Value>) -> Vec<Leaf> {
    if let (Some(Value::Object(bo)), Some(Value::Object(ho))) = (b, h) {
        let mut out = Vec::new();
        for key in keys(bo, ho) {
            let (bv, hv) = (bo.get(key), ho.get(key));
            if bv != hv {
                out.push(Leaf::new(
                    format!("{label} {}", humanize(key).to_lowercase()),
                    display(bv),
                    display(hv),
                ));
            }
        }
        return out;
    }
    vec![Leaf::new(label, display(b), display(h))]
}

/// Keys of both objects: `h`'s in its order, then those only `b` has.
pub(crate) fn keys<'a>(b: &'a Map<String, Value>, h: &'a Map<String, Value>) -> Vec<&'a str> {
    let mut out: Vec<&str> = h.keys().map(String::as_str).collect();
    out.extend(b.keys().map(String::as_str).filter(|k| !h.contains_key(*k)));
    out
}

/// A JSON value as the panel would show it.
pub(crate) fn display(v: Option<&Value>) -> Value {
    match v {
        None | Some(Value::Null) => "none".into(),
        Some(Value::Bool(b)) => yes_no(*b).into(),
        Some(Value::Number(n)) => n.as_f64().map_or(Value::Null, num),
        Some(Value::String(s)) => enum_text(s).into(),
        Some(Value::Array(a)) if a.is_empty() => "none".into(),
        Some(Value::Array(a)) if a.len() <= 4 && a.iter().all(Value::is_number) => Value::Array(
            a.iter()
                .map(|x| x.as_f64().map_or(Value::Null, num))
                .collect(),
        ),
        Some(Value::Array(a)) => list_text(a).into(),
        Some(v @ Value::Object(_)) => clip(&v.to_string(), 120).into(),
    }
}

/// A list of words as words; of objects, by what they are ("Drop shadow,
/// Layer blur"), or how many.
fn list_text(a: &[Value]) -> String {
    if let Some(words) = a.iter().map(Value::as_str).collect::<Option<Vec<_>>>() {
        return words
            .into_iter()
            .map(enum_text)
            .collect::<Vec<_>>()
            .join(", ");
    }
    let types: Vec<String> = a
        .iter()
        .filter_map(|x| x.get("type").and_then(Value::as_str).map(enum_text))
        .collect();
    if types.len() == a.len() {
        return types.join(", ");
    }
    if a.len() == 1 {
        return "1 item".into();
    }
    format!("{} items", a.len())
}

fn paint_text(p: &Paint) -> String {
    let mut s = match p.solid_color() {
        Some(c) => format!("#{}", c.to_hex()),
        None => p.label(),
    };
    if p.opacity < 1.0 {
        s += &format!(" {}%", text(&num(f64::from(p.opacity) * 100.0)));
    }
    if !p.visible {
        s += " (hidden)";
    }
    s
}

fn paint_list(ps: &[Paint]) -> String {
    if ps.is_empty() {
        return "none".into();
    }
    ps.iter().map(paint_text).collect::<Vec<_>>().join(", ")
}

/// Paints as JSON (a text run's fills), read back as paints.
fn paints_text(v: Option<&Value>) -> String {
    match v.map(|v| serde_json::from_value::<Vec<Paint>>(v.clone())) {
        Some(Ok(ps)) => paint_list(&ps),
        _ => "the layer's fill".into(),
    }
}

/// A colour as the `[r, g, b, a]` tuple the renderer draws a chip for.
fn rgba(c: Color) -> Value {
    let f = |x: f32| {
        let x = (f64::from(x) * 10_000.0).round() / 10_000.0;
        json!(x)
    };
    json!([f(c.r), f(c.g), f(c.b), f(c.a)])
}

fn color_value(v: Option<&Value>) -> Value {
    match v.map(|v| serde_json::from_value::<Color>(v.clone())) {
        Some(Ok(c)) => rgba(c),
        _ => "none".into(),
    }
}

fn layout_text(v: Option<&Value>) -> String {
    match v.and_then(|v| v.get("mode")).and_then(Value::as_str) {
        Some(mode) => enum_text(mode).to_lowercase(),
        None => "off".into(),
    }
}

fn count(v: Option<&Value>, one: &str, many: &str) -> String {
    match v.and_then(Value::as_array).map_or(0, Vec::len) {
        1 => format!("1 {one}"),
        k => format!("{k} {many}"),
    }
}

/// A number at the panel's precision: two decimals, and whole numbers whole.
pub(crate) fn num(x: f64) -> Value {
    if !x.is_finite() {
        return Value::Null;
    }
    let r = (x * 100.0).round() / 100.0;
    if r == r.trunc() && r.abs() < 1e15 {
        json!(r as i64)
    } else {
        json!(r)
    }
}

fn percent(x: f32) -> String {
    format!("{}%", text(&num(f64::from(x) * 100.0)))
}

fn shown(v: bool) -> &'static str {
    if v {
        "visible"
    } else {
        "hidden"
    }
}

fn locked(v: bool) -> &'static str {
    if v {
        "locked"
    } else {
        "unlocked"
    }
}

fn yes_no(v: bool) -> &'static str {
    if v {
        "yes"
    } else {
        "no"
    }
}

/// A value's text, for building other text from it.
fn text(v: &Value) -> String {
    match v {
        Value::String(s) => s.clone(),
        other => other.to_string(),
    }
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

/// A property's name for the panel: `ChildLayout` → "Child layout".
fn prop_label(p: Prop) -> String {
    match p {
        Prop::Blend => "Blend mode".into(),
        Prop::Stroke => "Stroke".into(),
        Prop::Grids => "Layout grids".into(),
        Prop::Exports => "Export settings".into(),
        Prop::Interactions => "Prototype interactions".into(),
        Prop::Styles => "Linked styles".into(),
        Prop::Bound => "Variable bindings".into(),
        Prop::VarModes => "Variable modes".into(),
        Prop::Source => "Main component".into(),
        Prop::Overrides => "Instance overrides".into(),
        other => humanize(&format!("{other:?}")),
    }
}

/// An identifier as words: `font_size`, `fontSize` and `FontSize` → "Font size".
pub(crate) fn humanize(id: &str) -> String {
    let mut words: Vec<String> = Vec::new();
    let mut cur = String::new();
    let mut prev_lower = false;
    for c in id.chars() {
        if c == '_' || c == '-' || c == ' ' {
            if !cur.is_empty() {
                words.push(std::mem::take(&mut cur));
            }
            prev_lower = false;
            continue;
        }
        if c.is_uppercase() && prev_lower && !cur.is_empty() {
            words.push(std::mem::take(&mut cur));
        }
        prev_lower = c.is_lowercase() || c.is_ascii_digit();
        cur.extend(c.to_lowercase());
    }
    if !cur.is_empty() {
        words.push(cur);
    }
    let mut s = words.join(" ");
    if let Some(first) = s.get(..1) {
        s = first.to_uppercase() + s.get(1..).unwrap_or_default();
    }
    s
}

/// An enum's serialized name as words ("passThrough" → "Pass through");
/// other text as it is.
fn enum_text(s: &str) -> String {
    let ident = !s.is_empty()
        && s.chars().all(|c| c.is_ascii_alphanumeric() || c == '_')
        && s.chars().next().is_some_and(|c| c.is_ascii_alphabetic());
    if ident && s.chars().any(|c| c.is_ascii_lowercase()) {
        humanize(s)
    } else {
        s.to_string()
    }
}

/// A label as a path segment: "Fill 2 colour" → "fill-2-colour".
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

/// Paints given as JSON (a paint style's), compared as paints.
pub(crate) fn paints_json_leaves(what: &str, b: Option<&Value>, h: Option<&Value>) -> Vec<Leaf> {
    let read = |v: Option<&Value>| {
        v.and_then(|v| serde_json::from_value::<Vec<Paint>>(v.clone()).ok())
            .unwrap_or_default()
    };
    paint_leaves(what, &read(b), &read(h))
}
