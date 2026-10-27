//! Both entry points against the Go SDK's own expectations: where the Go tests
//! (packages/go/fhr/cli_test.go, call_test.go) pin an exact string — a usage
//! line, an answer's JSON, an error a handler causes — these pin the same one,
//! so forge and ForgeHub cannot tell a Rust handler from a Go one.

use base64::engine::general_purpose::STANDARD as B64;
use base64::Engine as _;
use fhr::{
    call, decode_response, encode_request, run_cli, Capabilities, ChangeKind, DiffChange, Handler,
    Info, Merged, SemanticConflict, StructuredDiff,
};
use serde_json::json;

/// The Go tests' stub: fixed answers, so only the plumbing is exercised.
#[derive(Default)]
struct Stub {
    info: Option<Info>,
    conflicts: Vec<SemanticConflict>,
}

fn stub_info() -> Info {
    Info::new("stub", [".stub"])
}

impl Handler for Stub {
    fn info(&self) -> Info {
        self.info.clone().unwrap_or_else(stub_info)
    }

    fn diff(&self, _base: &[u8], head: &[u8]) -> Result<StructuredDiff, String> {
        if head == b"boom" {
            return Err("parsing head: boom".into());
        }
        if head == b"panic" {
            panic!("index out of range");
        }
        Ok(StructuredDiff::new(
            "stub",
            vec![DiffChange::new("a/b", ChangeKind::Renamed, "b")
                .before("x")
                .after("b")],
        ))
    }

    fn merge(&self, base: &[u8], ours: &[u8], theirs: &[u8]) -> Result<Merged, String> {
        Ok(Merged {
            blob: [base, ours, theirs].concat(),
            conflicts: self.conflicts.clone(),
        })
    }

    fn preview(&self, blob: &[u8]) -> Result<Vec<u8>, String> {
        if blob == b"boom" {
            return Err("parsing: boom".into());
        }
        Ok(blob.iter().rev().copied().collect())
    }

    fn apply_choices(
        &self,
        merged: &[u8],
        theirs: &[u8],
        take: &[String],
    ) -> Result<Vec<u8>, String> {
        let mut out = merged.to_vec();
        for p in take {
            if p != "nodes/A" {
                return Err(format!("unknown conflict path {p}"));
            }
            out.push(b'+');
            out.extend_from_slice(theirs);
        }
        Ok(out)
    }

    fn import(&self, blob: &[u8]) -> Result<Vec<u8>, String> {
        Ok(blob.to_ascii_uppercase())
    }

    fn export(&self, glb: &[u8], format: &str) -> Result<Vec<u8>, String> {
        Ok([glb.to_ascii_lowercase().as_slice(), format.as_bytes()].concat())
    }
}

fn with(info: Info) -> Stub {
    Stub {
        info: Some(info),
        ..Stub::default()
    }
}

/// Every optional call declared.
fn full() -> Stub {
    with(
        stub_info()
            .preview(fhr::MEDIA_TYPE_GLB)
            .capabilities(Capabilities {
                semantic_compare: true,
                semantic_merge: true,
                apply_choices: true,
                import: true,
                export: true,
            }),
    )
}

fn b64(s: &str) -> String {
    B64.encode(s)
}

fn cli(h: &dyn Handler, stdin: &str, args: &[&str]) -> (i32, String, String) {
    let (mut out, mut err) = (Vec::new(), Vec::new());
    let args: Vec<String> = args.iter().map(|s| s.to_string()).collect();
    let code = run_cli(h, &args, &mut stdin.as_bytes(), &mut out, &mut err);
    (
        code,
        String::from_utf8(out).unwrap(),
        String::from_utf8(err).unwrap(),
    )
}

// ─── the subprocess protocol ─────────────────────────────────────────────────

#[test]
fn usage_and_unknown_subcommand() {
    assert_eq!(
        cli(&Stub::default(), "", &[]),
        (
            1,
            String::new(),
            "usage: forge-handler-stub <match|diff|merge|info> [filepath]\n".into()
        )
    );
    assert_eq!(
        cli(&Stub::default(), "", &["bogus"]).2,
        "unknown subcommand: bogus\n"
    );
    // Optional subcommands appear only when declared, in the Go SDK's order.
    assert_eq!(
        cli(&full(), "", &[]).2,
        "usage: forge-handler-stub <match|diff|merge|apply-choices|preview|import|export|info> [filepath]\n"
    );
}

#[test]
fn matches_by_extension() {
    assert_eq!(cli(&Stub::default(), "", &["match", "x.stub"]).1, "true\n");
    assert_eq!(cli(&Stub::default(), "", &["match", "X.STUB"]).1, "true\n");
    assert_eq!(cli(&Stub::default(), "", &["match", "x.csv"]).1, "false\n");
    assert_eq!(
        cli(&Stub::default(), "", &["match"]),
        (0, "false\n".into(), String::new())
    );
}

#[test]
fn diff_decodes_blobs_and_encodes_the_diff() {
    let (code, out, _) = cli(
        &Stub::default(),
        &format!(r#"{{"base":"{}","head":"{}"}}"#, b64("a"), b64("b")),
        &["diff"],
    );
    assert_eq!(code, 0);
    assert_eq!(out, r#"{"version":"1.0","format":"stub","changes":[{"path":"a/b","kind":"renamed","label":"b","before":"x","after":"b"}]}"#.to_string() + "\n");
}

#[test]
fn errors_are_json_on_stderr() {
    let err = |stdin: &str, sub: &str| {
        let (code, out, err) = cli(&Stub::default(), stdin, &[sub]);
        assert_eq!((code, out.as_str()), (1, ""), "{sub} {stdin}");
        assert!(err.ends_with('\n'));
        serde_json::from_str::<serde_json::Value>(&err).unwrap()["error"]
            .as_str()
            .unwrap()
            .to_string()
    };
    assert!(err(r#"{"base":"!!","head":""}"#, "diff").starts_with("decoding base blob: "));
    assert!(err(r#"{"base":"","head":"!!"}"#, "diff").starts_with("decoding head blob: "));
    assert_eq!(
        err(
            &format!(r#"{{"base":"","head":"{}"}}"#, b64("boom")),
            "diff"
        ),
        "parsing head: boom"
    );
    assert!(
        err(r#"{"base":"","ours":"","theirs":"!"}"#, "merge").starts_with("decoding theirs blob: ")
    );
    assert!(!err("not json", "diff").is_empty());
}

#[test]
fn a_handler_panic_is_an_error_not_a_crash() {
    let (code, _, err) = cli(
        &Stub::default(),
        &format!(r#"{{"base":"","head":"{}"}}"#, b64("panic")),
        &["diff"],
    );
    assert_eq!(
        (code, err.as_str()),
        (1, "{\"error\":\"handler panicked: index out of range\"}\n")
    );
}

#[test]
fn merge() {
    let stdin = format!(
        r#"{{"base":"{}","ours":"{}","theirs":"{}"}}"#,
        b64("a"),
        b64("b"),
        b64("c")
    );
    assert_eq!(
        cli(&Stub::default(), &stdin, &["merge"]).1,
        format!(r#"{{"blob":"{}"}}"#, b64("abc")) + "\n"
    );
    let s = Stub {
        conflicts: vec![SemanticConflict {
            path: "p".into(),
            ours: json!(1),
            theirs: json!(2),
        }],
        ..Stub::default()
    };
    assert_eq!(
        cli(&s, &stdin, &["merge"]).1,
        format!(
            r#"{{"blob":"{}","conflicts":[{{"path":"p","ours":1,"theirs":2}}]}}"#,
            b64("abc")
        ) + "\n"
    );
}

#[test]
fn info_answers_the_declaration() {
    assert_eq!(
        cli(&Stub::default(), "", &["info"]).1,
        "{\"id\":\"stub\",\"formats\":[\".stub\"],\"protocol\":\"1.0\"}\n"
    );
    let caps = with(stub_info().capabilities(Capabilities {
        semantic_compare: true,
        ..Capabilities::default()
    }));
    assert_eq!(
        cli(&caps, "", &["info"]).1,
        r#"{"id":"stub","formats":[".stub"],"protocol":"1.0","capabilities":{"semanticCompare":true,"semanticMerge":false}}"#.to_string() + "\n"
    );
    let previewing = with(stub_info().preview(fhr::MEDIA_TYPE_GLB));
    assert_eq!(
        cli(&previewing, "", &["info"]).1,
        r#"{"id":"stub","formats":[".stub"],"protocol":"1.0","preview":"model/gltf-binary"}"#
            .to_string()
            + "\n"
    );
}

#[test]
fn preview_only_when_declared() {
    let p = with(stub_info().preview(fhr::MEDIA_TYPE_GLB));
    let stdin = format!(r#"{{"blob":"{}"}}"#, b64("abc"));
    assert_eq!(
        cli(&p, &stdin, &["preview"]).1,
        format!(
            r#"{{"mediaType":"model/gltf-binary","blob":"{}"}}"#,
            b64("cba")
        ) + "\n"
    );
    assert_eq!(
        cli(
            &p,
            &format!(r#"{{"blob":"{}"}}"#, b64("boom")),
            &["preview"]
        )
        .2,
        "{\"error\":\"parsing: boom\"}\n"
    );
    // Implemented but not declared is not offered.
    assert_eq!(
        cli(&Stub::default(), r#"{"blob":""}"#, &["preview"]).2,
        "{\"error\":\"stub has no preview: the format renders from its own bytes\"}\n"
    );
}

#[test]
fn apply_choices() {
    let stdin = format!(
        r#"{{"merged":"{}","theirs":"{}","take":["nodes/A"]}}"#,
        b64("m"),
        b64("t")
    );
    assert_eq!(
        cli(&full(), &stdin, &["apply-choices"]).1,
        format!(r#"{{"blob":"{}"}}"#, b64("m+t")) + "\n"
    );
    assert!(cli(
        &full(),
        r#"{"merged":"","theirs":"","take":["nodes/B"]}"#,
        &["apply-choices"]
    )
    .2
    .contains("unknown conflict path nodes/B"));
    assert!(cli(
        &Stub::default(),
        r#"{"merged":"","theirs":"","take":[]}"#,
        &["apply-choices"]
    )
    .2
    .contains("stub cannot apply conflict choices"));
}

#[test]
fn import_export_and_format_resolution() {
    let (code, out, _) = cli(
        &full(),
        &format!(r#"{{"blob":"{}"}}"#, b64("abc")),
        &["import"],
    );
    assert_eq!(
        (code, out),
        (
            0,
            format!(
                r#"{{"mediaType":"model/gltf-binary","blob":"{}"}}"#,
                b64("ABC")
            ) + "\n"
        )
    );
    let (code, out, _) = cli(
        &full(),
        &format!(r#"{{"blob":"{}","format":".stub"}}"#, b64("ABC")),
        &["export"],
    );
    assert_eq!(
        (code, out.trim()),
        (0, format!(r#"{{"blob":"{}"}}"#, b64("abc.stub")).as_str())
    );
    // An omitted format is the only one; several formats must be named.
    assert_eq!(
        cli(
            &full(),
            &format!(r#"{{"blob":"{}"}}"#, b64("A")),
            &["export"]
        )
        .0,
        0
    );
    let mut multi = full();
    multi.info.as_mut().unwrap().formats = vec![".a".into(), ".b".into()];
    assert!(cli(
        &multi,
        &format!(r#"{{"blob":"{}"}}"#, b64("A")),
        &["export"]
    )
    .2
    .contains("say which"));
    assert!(cli(
        &full(),
        &format!(r#"{{"blob":"{}","format":".nope"}}"#, b64("A")),
        &["export"]
    )
    .2
    .contains("does not export"));
    for sub in ["import", "export"] {
        assert!(
            cli(&Stub::default(), r#"{"blob":""}"#, &[sub])
                .2
                .contains("stub cannot"),
            "{sub}"
        );
    }
}

// ─── fhr-wasm-1 ──────────────────────────────────────────────────────────────

fn wasm(
    h: &dyn Handler,
    op: &str,
    params: serde_json::Value,
    blobs: &[&[u8]],
) -> Result<(serde_json::Value, Vec<Vec<u8>>), String> {
    decode_response(&call(h, &encode_request(op, params, blobs)))
}

#[test]
fn fhr_wasm_1_answers_as_the_subprocess_does() {
    let (result, blobs) = wasm(&Stub::default(), "diff", json!({}), &[b"a", b"b"]).unwrap();
    let (_, out, _) = cli(
        &Stub::default(),
        &format!(r#"{{"base":"{}","head":"{}"}}"#, b64("a"), b64("b")),
        &["diff"],
    );
    assert_eq!(serde_json::to_string(&result).unwrap() + "\n", out);
    assert!(blobs.is_empty());
    let (info, _) = wasm(&full(), "info", json!({}), &[]).unwrap();
    assert_eq!(
        serde_json::to_string(&info).unwrap() + "\n",
        cli(&full(), "", &["info"]).1
    );
    assert_eq!(
        wasm(&Stub::default(), "match", json!({"path": "a.stub"}), &[])
            .unwrap()
            .0,
        json!(true)
    );
}

#[test]
fn fhr_wasm_1_carries_blobs_raw() {
    let (result, blobs) = wasm(&Stub::default(), "merge", json!({}), &[b"b", b"o", b"t"]).unwrap();
    assert_eq!((result, blobs), (json!({}), vec![b"bot".to_vec()]));
    let (result, blobs) = wasm(&full(), "preview", json!({}), &[b"abc"]).unwrap();
    assert_eq!(
        (result, blobs),
        (
            json!({"mediaType": "model/gltf-binary"}),
            vec![b"cba".to_vec()]
        )
    );
    let (_, blobs) = wasm(
        &full(),
        "applyChoices",
        json!({"take": ["nodes/A"]}),
        &[b"m", b"t"],
    )
    .unwrap();
    assert_eq!(blobs, vec![b"m+t".to_vec()]);
    let (_, blobs) = wasm(&full(), "export", json!({}), &[b"ABC"]).unwrap();
    assert_eq!(blobs, vec![b"abc.stub".to_vec()]);
    // 9 MiB of every byte value, past the old 8 MiB cap, round-tripped exactly.
    let big: Vec<u8> = (0..9 << 20).map(|i| i as u8).collect();
    let (_, blobs) = wasm(&full(), "preview", json!({}), &[&big]).unwrap();
    assert!(blobs[0].iter().eq(big.iter().rev()));
}

#[test]
fn fhr_wasm_1_refuses_and_survives() {
    assert_eq!(
        wasm(&Stub::default(), "diff", json!({}), &[b"a", b"boom"]).unwrap_err(),
        "parsing head: boom"
    );
    assert_eq!(
        wasm(&Stub::default(), "diff", json!({}), &[b"a"]).unwrap_err(),
        "diff takes 2 blob(s) ([base head]), got 1"
    );
    assert_eq!(
        wasm(&Stub::default(), "frobnicate", json!({}), &[]).unwrap_err(),
        "unknown call \"frobnicate\""
    );
    assert_eq!(
        wasm(&Stub::default(), "preview", json!({}), &[b"x"]).unwrap_err(),
        "stub has no preview: the format renders from its own bytes"
    );
    assert_eq!(
        wasm(&Stub::default(), "diff", json!({}), &[b"a", b"panic"]).unwrap_err(),
        "handler panicked: index out of range"
    );
    assert!(wasm(&Stub::default(), "info", json!({}), &[]).is_ok());
    let header = |h: &str| [(h.len() as u32).to_le_bytes().as_slice(), h.as_bytes()].concat();
    for (req, want) in [
        (vec![1u8, 0], "request too short"),
        (header("nope"), "request header"),
        (
            header(r#"{"op":"diff","blobs":[3,10]}"#),
            "blob 0 (3 bytes) overruns",
        ),
        (
            [header(r#"{"op":"info","blobs":[]}"#), b"x".to_vec()].concat(),
            "1 bytes after the last blob",
        ),
        (
            header(r#"{"op":"diff","blobs":[-1]}"#),
            "blob 0 (-1 bytes) overruns",
        ),
    ] {
        let e = decode_response(&call(&Stub::default(), &req)).unwrap_err();
        assert!(e.contains(want), "{e:?} should contain {want:?}");
    }
}
