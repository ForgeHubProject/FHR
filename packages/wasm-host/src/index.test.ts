// The reference host against a real handler: the csv handler, built twice
// from the same source — as an fhr-wasm-1 module and as the native binary
// forge runs — must answer every call the same way. Building it needs Go 1.24
// or newer on PATH (go:wasmexport); CI installs one.

import { describe, it, expect, beforeAll } from "vitest";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { loadHandler, encodeRequest, decodeResponse, HandlerError, type WasmHandler } from "./index.js";

const handlerDir = fileURLToPath(new URL("../../handler-csv/", import.meta.url));
let wasm: Uint8Array;
let native: string;
let csv: WasmHandler;

beforeAll(async () => {
  const out = mkdtempSync(join(tmpdir(), "fhr-wasm-host-"));
  const go = (env: Record<string, string>, args: string[]) =>
    execFileSync("go", ["build", "-trimpath", ...args], { cwd: handlerDir, env: { ...process.env, CGO_ENABLED: "0", ...env }, stdio: "pipe" });
  go({ GOOS: "wasip1", GOARCH: "wasm" }, ["-buildmode=c-shared", "-o", join(out, "csv.fhr1.wasm"), "."]);
  go({}, ["-o", join(out, "csv-native"), "."]);
  wasm = readFileSync(join(out, "csv.fhr1.wasm"));
  native = join(out, "csv-native");
  csv = await loadHandler(wasm);
}, 180_000);

/** The native binary's answer to a subprocess call, as parsed JSON. */
function nativeCall(sub: string, input?: object): unknown {
  const stdout = execFileSync(native, [sub], { input: input ? JSON.stringify(input) : "", stdio: ["pipe", "pipe", "pipe"] });
  return JSON.parse(stdout.toString());
}

const enc = (s: string) => new TextEncoder().encode(s);
const b64 = (b: Uint8Array) => Buffer.from(b).toString("base64");

describe("fhr-wasm-1 host, with the csv handler", () => {
  it("answers info as the native binary does", () => {
    expect(csv.call("info").result).toEqual(nativeCall("info"));
  });

  it("answers diff as the native binary does", () => {
    const base = enc("name,qty\nbolt,4\nnut,10\n");
    const head = enc("name,qty\nbolt,6\nnut,10\nwasher,2\n");
    const { result, blobs } = csv.call("diff", [base, head]);
    expect(blobs).toEqual([]);
    expect(result).toEqual(nativeCall("diff", { base: b64(base), head: b64(head) }));
    expect(JSON.stringify(result)).toContain("washer");
  });

  it("answers match from the path parameter", () => {
    expect(csv.call("match", [], { path: "parts/bom.csv" }).result).toBe(true);
    expect(csv.call("match", [], { path: "parts/bom.txt" }).result).toBe(false);
  });

  it("returns a handler's refusal as a HandlerError, and stays usable", () => {
    expect(() => csv.call("diff", [enc("a")])).toThrow(HandlerError);
    expect(() => csv.call("frobnicate")).toThrow(/unknown call "frobnicate"/);
    expect(csv.call("info").result).toMatchObject({ id: "csv" });
  });

  it("takes inputs past the old 8 MiB cap", () => {
    const base = bigCsv(9 << 20, "a");
    const head = bigCsv(9 << 20, "b");
    expect(base.length).toBeGreaterThan(8 << 20);
    const { result } = csv.call("diff", [base, head]);
    expect(JSON.stringify(result)).toContain('"kind":"modified"');
  });

  it("frees what it allocates: repeated calls don't grow memory", async () => {
    const h = await loadHandler(wasm);
    const base = bigCsv(1 << 20, "a");
    const head = bigCsv(1 << 20, "b");
    for (let i = 0; i < 5; i++) h.call("diff", [base, head]);
    const warm = h.memoryBytes;
    for (let i = 0; i < 30; i++) h.call("diff", [base, head]);
    // Leaking each call's 2 MB request would add 60 MB; freed, the memory stays
    // near its warm size (some slack for the collector).
    expect(h.memoryBytes - warm).toBeLessThan(16 << 20);
  });
});

/** A two-row CSV whose one cell is `size` bytes ending in `last`: big, but cheap to diff. */
function bigCsv(size: number, last: string): Uint8Array {
  return enc("name,data\nrow," + "x".repeat(size - 1) + last + "\n");
}

describe("loading", () => {
  it("refuses a module that isn't fhr-wasm-1", async () => {
    const empty = new Uint8Array([0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00]);
    await expect(loadHandler(empty)).rejects.toThrow("not an fhr-wasm-1 module: it does not export memory");
  });
});

describe("the envelope", () => {
  it("round-trips a request and a response", () => {
    const req = encodeRequest("export", [enc("glb")], { format: ".obj" });
    const n = new DataView(req.buffer).getUint32(0, true);
    expect(JSON.parse(new TextDecoder().decode(req.subarray(4, 4 + n)))).toEqual({ format: ".obj", op: "export", blobs: [3] });
    expect(new TextDecoder().decode(req.subarray(4 + n))).toBe("glb");

    const header = enc(JSON.stringify({ result: { mediaType: "model/gltf-binary" }, blobs: [2, 1] }));
    const resp = new Uint8Array([...new Uint8Array(new Uint32Array([header.length]).buffer), ...header, 7, 8, 9]);
    const { result, blobs } = decodeResponse(resp);
    expect(result).toEqual({ mediaType: "model/gltf-binary" });
    expect(blobs.map((b) => [...b])).toEqual([[7, 8], [9]]);
  });

  it("turns an error response into a HandlerError", () => {
    const header = enc(JSON.stringify({ error: "csv: bad header" }));
    const resp = new Uint8Array([...new Uint8Array(new Uint32Array([header.length]).buffer), ...header]);
    expect(() => decodeResponse(resp)).toThrow(new HandlerError("csv: bad header"));
  });
});
