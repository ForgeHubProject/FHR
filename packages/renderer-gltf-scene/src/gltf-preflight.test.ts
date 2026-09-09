import { describe, it, expect } from "vitest";
import { decodeGltf, type GltfDocument } from "./gltf-parse.js";
import { preflightGltf, unreadablePreflight, MESHOPT } from "./gltf-preflight.js";
import { buildGltf, toGlb, toGltfJson } from "./glb-fixture.js";

/** Preflight a fixture the way production does: decode the bytes, then classify. */
const preflightBytes = (bytes: Uint8Array) => preflightGltf(decodeGltf(bytes));

describe("preflight: the degradation matrix", () => {
  it("self-contained GLB → load the real model, say nothing", () => {
    const pf = preflightBytes(toGlb(buildGltf()));
    expect(pf.canLoadModel).toBe(true);
    expect(pf.degradation).toBe("none");
    expect(pf.banners).toEqual([]);
    expect(pf.needsMeshopt).toBe(false);
  });

  it(".gltf with a data: uri buffer counts as embedded, not external", () => {
    const pf = preflightBytes(toGltfJson(buildGltf()));
    expect(pf.externalBuffers).toEqual([]);
    expect(pf.canLoadModel).toBe(true);
  });

  it("external .bin → refuse the model, name the sibling file", () => {
    const pf = preflightBytes(toGltfJson(buildGltf({ externalBufferUri: "geometry/scene.bin" })));
    expect(pf.canLoadModel).toBe(false);
    expect(pf.degradation).toBe("external-buffer");
    expect(pf.externalBuffers).toEqual(["scene.bin"]);
    expect(pf.banners[0]).toContain("needs sibling scene.bin");
    expect(pf.banners[0]).toContain("scene-graph outline");
  });

  it("external textures → still load geometry, but count them in a banner", () => {
    const pf = preflightBytes(
      toGlb(buildGltf({ externalImageUris: ["textures/paint.png", "textures/trim.jpg"] })),
    );
    expect(pf.canLoadModel).toBe(true);
    expect(pf.degradation).toBe("none");
    expect(pf.externalImages).toEqual(["paint.png", "trim.jpg"]);
    expect(pf.banners).toHaveLength(1);
    expect(pf.banners[0]).toContain("2 textures live in separate files");
    expect(pf.banners[0]).toContain("untextured");
  });

  it("one external texture is described in the singular", () => {
    const pf = preflightBytes(toGlb(buildGltf({ externalImageUris: ["paint.png"] })));
    expect(pf.banners[0]).toContain("1 texture lives");
  });

  it("a .glb may carry external images — GLB does not imply self-contained", () => {
    const pf = preflightBytes(toGlb(buildGltf({ externalImageUris: ["a.png"] })));
    expect(pf.externalImages).toEqual(["a.png"]);
  });

  it("embedded (bufferView) images are not external", () => {
    const pf = preflightBytes(toGlb(buildGltf({ embeddedImage: true })));
    expect(pf.externalImages).toEqual([]);
    expect(pf.banners).toEqual([]);
  });

  it("required Draco → never call the loader; degrade with a named reason", () => {
    const pf = preflightBytes(
      toGlb(buildGltf({ extensionsRequired: ["KHR_draco_mesh_compression"], extensionsUsed: ["KHR_draco_mesh_compression"] })),
    );
    expect(pf.canLoadModel).toBe(false);
    expect(pf.degradation).toBe("unsupported-extension");
    expect(pf.unsupportedExtensions).toEqual(["KHR_draco_mesh_compression"]);
    expect(pf.banners[0]).toContain("Draco-compressed geometry");
    expect(pf.banners[0]).toContain("KHR_draco_mesh_compression");
    expect(pf.banners[0]).toContain("scene-graph outline");
  });

  it("required KTX2 → same degradation, its own plain-language name", () => {
    const pf = preflightBytes(toGlb(buildGltf({ extensionsRequired: ["KHR_texture_basisu"] })));
    expect(pf.canLoadModel).toBe(false);
    expect(pf.banners[0]).toContain("KTX2/Basis-compressed textures");
  });

  it("required meshopt → load the model and opt the decoder in", () => {
    const pf = preflightBytes(toGlb(buildGltf({ extensionsRequired: [MESHOPT], extensionsUsed: [MESHOPT] })));
    expect(pf.canLoadModel).toBe(true);
    expect(pf.needsMeshopt).toBe(true);
    expect(pf.unsupportedExtensions).toEqual([]);
    expect(pf.banners).toEqual([]);
  });

  it("meshopt listed only as used still attaches the decoder", () => {
    const pf = preflightBytes(toGlb(buildGltf({ extensionsUsed: [MESHOPT] })));
    expect(pf.needsMeshopt).toBe(true);
    expect(pf.canLoadModel).toBe(true);
  });

  it("an unknown extension that is only *used* is not a refusal", () => {
    const pf = preflightBytes(toGlb(buildGltf({ extensionsUsed: ["VENDOR_secret_sauce"] })));
    expect(pf.canLoadModel).toBe(true);
    expect(pf.unsupportedExtensions).toEqual([]);
  });

  it("an unknown *required* extension is refused by name", () => {
    const pf = preflightBytes(toGlb(buildGltf({ extensionsRequired: ["VENDOR_secret_sauce"] })));
    expect(pf.canLoadModel).toBe(false);
    expect(pf.unsupportedExtensions).toEqual(["VENDOR_secret_sauce"]);
    expect(pf.banners[0]).toContain("VENDOR_secret_sauce");
  });

  it("known-supported extensions (quantization, transforms, lights) load normally", () => {
    const pf = preflightBytes(
      toGlb(buildGltf({ extensionsRequired: ["KHR_mesh_quantization", "KHR_texture_transform", "KHR_lights_punctual"] })),
    );
    expect(pf.canLoadModel).toBe(true);
    expect(pf.banners).toEqual([]);
  });

  it("an unsupported extension outranks an external buffer in the banner", () => {
    const pf = preflightBytes(
      toGltfJson(buildGltf({ extensionsRequired: ["KHR_draco_mesh_compression"], externalBufferUri: "scene.bin" })),
    );
    expect(pf.degradation).toBe("unsupported-extension");
    expect(pf.banners).toHaveLength(1);
  });

  it("a refusal suppresses the texture warning (the model isn't drawn anyway)", () => {
    const pf = preflightBytes(
      toGltfJson(buildGltf({ externalBufferUri: "scene.bin", externalImageUris: ["a.png"] })),
    );
    expect(pf.banners).toHaveLength(1);
    expect(pf.banners[0]).toContain("needs sibling scene.bin");
  });

  it("lists at most three sibling names, then counts the rest", () => {
    const doc: GltfDocument = {
      buffers: [{ uri: "a.bin" }, { uri: "b.bin" }, { uri: "c.bin" }, { uri: "d.bin" }, { uri: "e.bin" }],
    };
    expect(preflightGltf(doc).banners[0]).toContain("a.bin, b.bin, c.bin and 2 more");
  });

  it("percent-encoded sibling names are shown decoded", () => {
    expect(preflightGltf({ buffers: [{ uri: "my%20model.bin" }] }).externalBuffers).toEqual(["my model.bin"]);
  });

  it("an empty document is loadable (nothing to refuse)", () => {
    const pf = preflightGltf({});
    expect(pf.canLoadModel).toBe(true);
    expect(pf.banners).toEqual([]);
  });

  it("undecodable bytes get an unreadable verdict with the reason", () => {
    const pf = unreadablePreflight("Unexpected token");
    expect(pf.canLoadModel).toBe(false);
    expect(pf.degradation).toBe("unreadable");
    expect(pf.banners[0]).toContain("Unexpected token");
  });
});
