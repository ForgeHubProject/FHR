//! Wire types — @fhr/types and the StructuredDiff JSON schema (SPEC.md), field
//! for field as the Go SDK writes them.

use serde::{Deserialize, Serialize};
use serde_json::Value;

/// What happened to one element.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ChangeKind {
    Added,
    Removed,
    Modified,
    /// One element that kept its identity and changed its name.
    Renamed,
    /// One node that kept its identity and moved to a different parent.
    Reparented,
}

/// One semantic unit of change. `before`/`after` are any JSON value; a change
/// with children is a group.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct DiffChange {
    pub path: String,
    pub kind: ChangeKind,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub label: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub before: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub after: Option<Value>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub children: Vec<DiffChange>,
}

impl DiffChange {
    /// A change of `kind` at `path`, labelled `label`.
    pub fn new(path: impl Into<String>, kind: ChangeKind, label: impl Into<String>) -> Self {
        DiffChange {
            path: path.into(),
            kind,
            label: label.into(),
            before: None,
            after: None,
            children: Vec::new(),
        }
    }

    /// With a before value.
    pub fn before(mut self, v: impl Into<Value>) -> Self {
        self.before = Some(v.into());
        self
    }

    /// With an after value.
    pub fn after(mut self, v: impl Into<Value>) -> Self {
        self.after = Some(v.into());
        self
    }

    /// With children: a group.
    pub fn children(mut self, children: Vec<DiffChange>) -> Self {
        self.children = children;
        self
    }
}

/// What `diff` answers.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct StructuredDiff {
    pub version: String,
    pub format: String,
    pub changes: Vec<DiffChange>,
}

impl StructuredDiff {
    /// A version-1.0 diff of `format` holding `changes`.
    pub fn new(format: impl Into<String>, changes: Vec<DiffChange>) -> Self {
        StructuredDiff {
            version: "1.0".into(),
            format: format.into(),
            changes,
        }
    }
}

/// One conflict a merge could not reconcile, at a diff path.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct SemanticConflict {
    pub path: String,
    pub ours: Value,
    pub theirs: Value,
}

/// What `merge` answers: the merged blob (ours at every conflict) and the conflicts.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Merged {
    pub blob: Vec<u8>,
    pub conflicts: Vec<SemanticConflict>,
}

/// The `info` call's answer, and the handler's declaration of what it does.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct Info {
    pub id: String,
    pub formats: Vec<String>,
    /// Filled in with [`crate::PROTOCOL_VERSION`] when left empty.
    #[serde(default)]
    pub protocol: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub capabilities: Option<Capabilities>,
    /// The media type `preview` produces; `None` for a handler without one.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub preview: Option<String>,
}

impl Info {
    /// A handler `id` claiming `formats` (extensions with their dot).
    pub fn new<S: Into<String>>(
        id: impl Into<String>,
        formats: impl IntoIterator<Item = S>,
    ) -> Self {
        Info {
            id: id.into(),
            formats: formats.into_iter().map(Into::into).collect(),
            ..Info::default()
        }
    }

    /// Declares the capabilities.
    pub fn capabilities(mut self, c: Capabilities) -> Self {
        self.capabilities = Some(c);
        self
    }

    /// Declares a preview of `media_type`.
    pub fn preview(mut self, media_type: impl Into<String>) -> Self {
        self.preview = Some(media_type.into());
        self
    }

    pub(crate) fn with_defaults(mut self) -> Self {
        if self.protocol.is_empty() {
            self.protocol = crate::PROTOCOL_VERSION.to_string();
        }
        self
    }

    pub(crate) fn can(&self, flag: fn(&Capabilities) -> bool) -> bool {
        self.capabilities.as_ref().is_some_and(flag)
    }
}

/// What the handler supports — the HandlerCapabilities object of @fhr/types.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Capabilities {
    pub semantic_compare: bool,
    pub semantic_merge: bool,
    #[serde(default, skip_serializing_if = "is_false")]
    pub apply_choices: bool,
    #[serde(default, skip_serializing_if = "is_false")]
    pub import: bool,
    #[serde(default, skip_serializing_if = "is_false")]
    pub export: bool,
}

fn is_false(b: &bool) -> bool {
    !*b
}
