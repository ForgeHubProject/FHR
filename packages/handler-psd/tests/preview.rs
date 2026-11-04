//! The preview: the flattened image as a PNG, and the chunk that says where
//! each layer is on it.

mod common;

use common::{Layer, Psd};
use fhr::Handler;
use handler_psd::{Psd as Handler_, CHUNK_KEYWORD};
use serde_json::{json, Value};

/// The PNG's size, its layout chunk and its RGBA pixels.
fn read(png: &[u8]) -> ((u32, u32), Value, Vec<u8>) {
    let mut layout = Value::Null;
    let mut i = 8;
    while i + 8 <= png.len() {
        let len = u32::from_be_bytes(png[i..i + 4].try_into().unwrap()) as usize;
        let (kind, data) = (&png[i + 4..i + 8], &png[i + 8..i + 8 + len]);
        if kind == b"tEXt" {
            let nul = data.iter().position(|b| *b == 0).unwrap();
            if &data[..nul] == CHUNK_KEYWORD.as_bytes() {
                layout = serde_json::from_slice(&data[nul + 1..]).unwrap();
            }
        }
        i += 12 + len;
    }
    let decoder = png::Decoder::new(std::io::Cursor::new(png));
    let mut reader = decoder.read_info().unwrap();
    let mut pixels = vec![0; reader.output_buffer_size().unwrap()];
    let info = reader.next_frame(&mut pixels).unwrap();
    ((info.width, info.height), layout, pixels)
}

#[test]
fn the_flattened_image_with_every_layers_box() {
    let doc = Psd::new(
        400,
        300,
        vec![
            Layer::text(
                10,
                "Hello",
                (40.0, 120.0),
                "Inter",
                48.0,
                [1.0, 1.0, 1.0],
                0.0,
            ),
            Layer::group(
                20,
                "Badges",
                vec![Layer::pixel(
                    21,
                    "Sticker",
                    (300, 20, 360, 80),
                    [255, 200, 0, 255],
                )],
            ),
            Layer::shape(
                6,
                "Strip",
                (0.0, 90.0, 400.0, 190.0),
                (400, 300),
                [0.0, 0.0, 0.0],
            ),
            Layer::pixel(4, "Paper", (0, 0, 400, 300), [20, 40, 60, 255]),
        ],
    );
    let ((w, h), layout, pixels) = read(&Handler_.preview(&doc.save()).unwrap());
    assert_eq!((w, h), (400, 300));
    assert_eq!(layout["scale"], json!(1.0));
    assert_eq!(layout["layers"]["21"], json!([300.0, 20.0, 60.0, 60.0]));
    // A group's box holds its layers'.
    assert_eq!(layout["layers"]["20"], layout["layers"]["21"]);
    // A shape's box is its path's.
    assert_eq!(layout["layers"]["6"], json!([0.0, 90.0, 400.0, 100.0]));
    assert!(layout["layers"]["10"].is_array());
    let px = |x: usize, y: usize| &pixels[(y * 400 + x) * 4..(y * 400 + x) * 4 + 4];
    assert_eq!(px(330, 50), [255, 200, 0, 255], "the sticker");
    assert_eq!(px(10, 250), [20, 40, 60, 255], "the paper");
}

#[test]
fn a_file_that_is_not_photoshop_is_an_error() {
    assert!(Handler_.preview(b"not a psd").is_err());
}
