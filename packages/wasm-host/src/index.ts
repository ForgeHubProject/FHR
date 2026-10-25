// The host side of fhr-wasm-1 (SPEC §7): load a handler module, whatever
// language built it, and call it. Runs unchanged in Node (20+) and browsers;
// it is the reference ForgeHub's loaders follow.
//
// The module exports fhr_alloc, fhr_free and fhr_call over its memory. A
// request and a response are both [u32 header length][JSON header][blobs], the
// blobs as raw bytes. A module may import a sandboxed WASI preview 1, which
// this host provides with no files, no network, an empty environment and no
// arguments; anything else a module imports is refused.

/** What one call answers: the result JSON and any files it carries. */
export type CallResult = { result: unknown; blobs: Uint8Array[] };

/** An answer of {"error": …}: the handler refused or failed the call. */
export class HandlerError extends Error {
  override name = "HandlerError";
}

export type WasmHandler = {
  /** One call: `diff` takes [base, head], `merge` [base, ours, theirs], and so on (SPEC §7). */
  call(op: string, blobs?: Uint8Array[], params?: Record<string, unknown>): CallResult;
  /** The module's memory in bytes. Wasm memory only grows, so this is its high-water mark. */
  readonly memoryBytes: number;
};

export type LoadOptions = {
  /** Lines the module writes to stdout or stderr (a Go panic's trace, say). */
  onLog?: (line: string) => void;
};

type Exports = {
  memory: WebAssembly.Memory;
  fhr_alloc(size: number): number;
  fhr_free(ptr: number, size: number): void;
  fhr_call(ptr: number, size: number): bigint;
  _initialize?: () => void;
};

const REQUIRED = ["memory", "fhr_alloc", "fhr_free", "fhr_call"] as const;

/** Load an fhr-wasm-1 module from its bytes (or an already compiled module). */
export async function loadHandler(source: BufferSource | WebAssembly.Module, options: LoadOptions = {}): Promise<WasmHandler> {
  const module = source instanceof WebAssembly.Module ? source : await WebAssembly.compile(source);
  const names = new Set(WebAssembly.Module.exports(module).map((e) => e.name));
  for (const name of REQUIRED) {
    if (!names.has(name)) throw new Error(`not an fhr-wasm-1 module: it does not export ${name}`);
  }
  let memory: WebAssembly.Memory | undefined;
  const imports: WebAssembly.Imports = {};
  for (const i of WebAssembly.Module.imports(module)) {
    if (i.module !== "wasi_snapshot_preview1") {
      throw new Error(`the module imports ${i.module}.${i.name}; fhr-wasm-1 hosts provide only a sandboxed WASI`);
    }
  }
  if (WebAssembly.Module.imports(module).length) {
    imports["wasi_snapshot_preview1"] = sandboxWasi(() => {
      if (!memory) throw new Error("the module called WASI before its memory was ready");
      return memory;
    }, options.onLog ?? (() => {}));
  }
  const instance = await WebAssembly.instantiate(module, imports);
  const x = instance.exports as unknown as Exports;
  memory = x.memory;
  x._initialize?.();

  return {
    get memoryBytes() {
      return x.memory.buffer.byteLength;
    },
    call(op, blobs = [], params = {}) {
      const request = encodeRequest(op, blobs, params);
      const ptr = x.fhr_alloc(request.length) >>> 0;
      // Every call into the module may grow its memory: view it afresh each time.
      new Uint8Array(x.memory.buffer, ptr, request.length).set(request);
      let packed: bigint;
      try {
        packed = BigInt.asUintN(64, x.fhr_call(ptr, request.length));
      } finally {
        x.fhr_free(ptr, request.length);
      }
      const outPtr = Number(packed >> 32n);
      const outLen = Number(packed & 0xffffffffn);
      const response = new Uint8Array(x.memory.buffer, outPtr, outLen).slice();
      x.fhr_free(outPtr, outLen);
      return decodeResponse(response);
    },
  };
}

const encoder = new TextEncoder();
const decoder = new TextDecoder();

/** [u32 header length][JSON header][blobs]: the request a module reads. */
export function encodeRequest(op: string, blobs: Uint8Array[], params: Record<string, unknown> = {}): Uint8Array {
  const header = encoder.encode(JSON.stringify({ ...params, op, blobs: blobs.map((b) => b.length) }));
  const out = new Uint8Array(4 + header.length + blobs.reduce((n, b) => n + b.length, 0));
  new DataView(out.buffer).setUint32(0, header.length, true);
  out.set(header, 4);
  let at = 4 + header.length;
  for (const b of blobs) {
    out.set(b, at);
    at += b.length;
  }
  return out;
}

/** A response's result and blobs; its {"error": …} becomes a HandlerError. */
export function decodeResponse(response: Uint8Array): CallResult {
  if (response.length < 4) throw new Error(`fhr-wasm-1 response too short: ${response.length} bytes`);
  const view = new DataView(response.buffer, response.byteOffset, response.byteLength);
  const n = view.getUint32(0, true);
  if (n > response.length - 4) throw new Error("fhr-wasm-1 response header overruns the response");
  const header = JSON.parse(decoder.decode(response.subarray(4, 4 + n))) as { result?: unknown; error?: string; blobs?: number[] };
  if (typeof header.error === "string") throw new HandlerError(header.error);
  const blobs: Uint8Array[] = [];
  let at = 4 + n;
  for (const len of header.blobs ?? []) {
    if (len < 0 || at + len > response.length) throw new Error("fhr-wasm-1 response blob overruns the response");
    blobs.push(response.subarray(at, at + len));
    at += len;
  }
  return { result: header.result, blobs };
}

// WASI errno values the sandbox answers with.
const ESUCCESS = 0;
const EBADF = 8;
const ENOSYS = 52;

/**
 * The WASI preview 1 a handler gets: time, randomness and somewhere to write
 * diagnostics, and nothing else. No files (there are no preopened directories),
 * no network, no environment and no arguments. Every other function answers
 * ENOSYS. proc_exit throws, so a module that exits (Go does on an unrecovered
 * panic) ends the call instead of the host.
 */
export function sandboxWasi(memory: () => WebAssembly.Memory, onLog: (line: string) => void): Record<string, (...args: never[]) => number> {
  const view = () => new DataView(memory().buffer);
  const pending: Record<number, string> = { 1: "", 2: "" };
  const known: Record<string, (...args: number[]) => number> = {
    args_sizes_get: (count, size) => (view().setUint32(count, 0, true), view().setUint32(size, 0, true), ESUCCESS),
    args_get: () => ESUCCESS,
    environ_sizes_get: (count, size) => (view().setUint32(count, 0, true), view().setUint32(size, 0, true), ESUCCESS),
    environ_get: () => ESUCCESS,
    clock_time_get: (id, _precisionLo, out) => {
      // 0 is the wall clock; the others are monotonic.
      const ns = id === 0 ? BigInt(Date.now()) * 1_000_000n : BigInt(Math.round(performance.now() * 1e6));
      view().setBigUint64(out, ns, true);
      return ESUCCESS;
    },
    random_get: (ptr, len) => {
      const bytes = new Uint8Array(memory().buffer, ptr, len);
      for (let at = 0; at < len; at += 65536) crypto.getRandomValues(bytes.subarray(at, Math.min(len, at + 65536)));
      return ESUCCESS;
    },
    fd_write: (fd, iovs, iovsLen, written) => {
      let total = 0;
      for (let i = 0; i < iovsLen; i++) {
        const ptr = view().getUint32(iovs + i * 8, true);
        const len = view().getUint32(iovs + i * 8 + 4, true);
        if (fd === 1 || fd === 2) pending[fd] += decoder.decode(new Uint8Array(memory().buffer, ptr, len));
        total += len;
      }
      for (const f of [1, 2]) {
        const lines = pending[f]!.split("\n");
        pending[f] = lines.pop()!;
        for (const line of lines) onLog(line);
      }
      view().setUint32(written, total, true);
      return fd === 1 || fd === 2 ? ESUCCESS : EBADF;
    },
    sched_yield: () => ESUCCESS,
    // Nothing to wait for in a sandbox: report no events, so a sleeping module
    // re-checks its clock rather than blocking the host.
    poll_oneoff: (_in, _out, _n, nevents) => (view().setUint32(nevents, 0, true), ESUCCESS),
    proc_exit: (code) => {
      throw new Error(`the handler exited with code ${code}`);
    },
    // No preopened directories: the first query already answers "no more".
    fd_prestat_get: () => EBADF,
  };
  const wasi: Record<string, (...args: never[]) => number> = {};
  const all = new Proxy(known, { get: (k, name: string) => k[name] ?? (() => ENOSYS) });
  for (const name of WASI_P1) wasi[name] = all[name] as (...args: never[]) => number;
  return wasi;
}

// Every WASI preview 1 function, so any module that imports one links.
const WASI_P1 = [
  "args_get", "args_sizes_get", "environ_get", "environ_sizes_get", "clock_res_get", "clock_time_get",
  "fd_advise", "fd_allocate", "fd_close", "fd_datasync", "fd_fdstat_get", "fd_fdstat_set_flags",
  "fd_fdstat_set_rights", "fd_filestat_get", "fd_filestat_set_size", "fd_filestat_set_times", "fd_pread",
  "fd_prestat_get", "fd_prestat_dir_name", "fd_pwrite", "fd_read", "fd_readdir", "fd_renumber", "fd_seek",
  "fd_sync", "fd_tell", "fd_write", "path_create_directory", "path_filestat_get", "path_filestat_set_times",
  "path_link", "path_open", "path_readlink", "path_remove_directory", "path_rename", "path_symlink",
  "path_unlink_file", "poll_oneoff", "proc_exit", "proc_raise", "sched_yield", "random_get", "sock_accept",
  "sock_recv", "sock_send", "sock_shutdown",
];
