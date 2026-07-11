# FHR Rendering & Compute Tiers Specification

**Status: DRAFT — for discussion.** Companion to [SPEC.md](./SPEC.md). This
document extends §8 (Frontend Renderer Contract) with a distribution and
serving model, adds WASM handler builds as a platform asset, and defines a
tiered model for *where* diffs are computed and rendered. Where this document
and SPEC.md §11 disagree on versioning, the content-hash rolling-release
scheme (FHR#4, forge#21/#23) is authoritative.

Implementation is intentionally out of scope; see the tracking issues:

- FHR: renderer distribution + WASM builds (this repo)
- forge: `forge diff --web` local renderer serving
- ForgeHub: tiered rendering + client-side render toggle

---

## 1. Motivation

Three observations from project review:

1. **The FHR → ForgeHub renderer handover is too direct.** ForgeHub is the
   only consumer of renderers, so the renderer contract never has to be truly
   consumer-agnostic, and forge — the tool that actually computes semantic
   diffs — has the weakest way to display them (terminal text).
2. **Forge should expose renderers itself.** The `git difftool` analogy:
   when a GUI is wanted, forge should serve the format's renderer to a local
   browser instead of expecting a desktop differ. Renderers are TS/JS; the
   browser is their natural runtime, and forge already owns the distribution
   pipeline (manifest → assets → `~/.forge/`).
3. **Server cost and client capability pull in opposite directions.**
   Client-side compute reduces server load but forces full blob downloads
   and repeats work on every viewer's machine — including weak ones.
   Server-side compute is cacheable and canonical but can be slow or costly.
   Neither should be hardwired.

Design goal: **where computation and rendering happen becomes a deployment /
runtime decision, not an architecture decision.** The enabler is a renderer
contract that consumes a `StructuredDiff` without caring who computed it.

## 2. Renderer bundle distribution

### 2a. Authoring floor (unchanged)

Handler authors keep writing the SPEC.md §8 floor: three React/TypeScript
components (`SnapshotRenderer`, `DiffRenderer`, `MergeResolver`) exported via
an `FHRManifest` object.

### 2b. Build output: self-contained ESM with a `mount()` wrapper

The package build step wraps the React floor in a framework-agnostic entry
point and inlines all dependencies (React, three.js, etc.):

```ts
// renderer.js — generated wrapper, single self-contained ESM file
export default {
  fhrVersion: 1,
  handlerId: "gltf-scene",
  build: "20260709-f1dd134",          // same content-hash build as the binaries
  mount(el: HTMLElement, props: MountProps): { update(p: MountProps): void; unmount(): void };
};

type MountProps = {
  mode: "view" | "diff" | "merge";
  diff?: StructuredDiff;              // computed by ANY tier (§4)
  blobs?: { base?: BlobRef; head?: BlobRef; ours?: BlobRef; theirs?: BlobRef };
  theme?: "light" | "dark";
  onEvent?: (e: RendererEvent) => void;   // e.g. merge resolution produced
};

type BlobRef = { url: string; size: number };  // consumer-served, same-origin
```

Rationale:

- **Framework-agnostic consumers.** forge's local shell page (§3b) must not
  need to be a React app; ForgeHub must not be pinned to the React version a
  bundle was built against. Only the wrapper boundary is standardized.
- **Self-contained = no runtime network.** The bundle makes no external
  fetches; blobs and diff JSON are provided by the consumer. This is what
  makes the same bundle work on ForgeHub, on `localhost` from forge, and
  offline.
- Bundle size is the accepted cost. Guidance: warn in CI above 3 MB gzipped;
  three.js-class dependencies are expected for 3D formats.

### 2c. Manifest schema additions

Two additions to the §4 manifest, both following the post-FHR#4 flat shape:

```toml
# WASM build of the backend handler — one more platform key, same table.
[assets.handlers."gltf-scene"]
"linux-amd64" = "https://github.com/forgehubproject/fhr/releases/download/gltf-scene-latest/forge-handler-gltf-scene-linux-amd64"
# ... existing platforms ...
"wasm"        = "https://github.com/forgehubproject/fhr/releases/download/gltf-scene-latest/forge-handler-gltf-scene.wasm"

# Renderer bundle — not platform-keyed; one ESM target.
[assets.renderers]
"gltf-scene" = "https://github.com/forgehubproject/fhr/releases/download/gltf-scene-latest/renderer-gltf-scene.js"
```

The existing rolling-release workflow builds and attaches both artifacts on
every push to the handler package; the `update-manifest` job stamps the same
build SHA into binaries, WASM, and renderer. **Backend, WASM, and renderer
are always released together under one content-hash build** — this preserves
the "no version skew between StructuredDiff producer and consumer" guarantee
of SPEC.md §11 without semver.

WASM builds come from the same Go source (`GOOS=js GOARCH=wasm`, or TinyGo if
size demands it). A handler package MAY omit the `wasm` key; consumers then
simply cannot offer in-browser compute for that format (§4, Tier B).

### 2d. Installation and pinning

`forge formats add <ext>` additionally downloads the renderer bundle to
`~/.forge/renderers/<handlerID>.js` (+ `.json` sidecar meta, mirroring
`~/.forge/plugins/`). The per-repo `.forge/handlers` lockfile entry pins one
build for binary + WASM + renderer jointly — no separate renderer pin.

## 3. Consumers

### 3a. ForgeHub web

Dynamically imports the renderer bundle for a file's handler (URL from the
repo's pinned build, cached by content hash) and calls `mount()`. Over time
this **replaces the hardcoded viewers** in `apps/web`; the built-in gltf/text
viewers become the first two published bundles. Sandboxing for community
(non-official) bundles remains an open question (§7, SPEC.md open question 5).

### 3b. forge local web UI — `forge diff --web`

```
forge diff <file> [<base>..<head>] --web
```

1. Resolves the handler + renderer for the file (per-repo scope via
   `.forge/formats`, pinned build via `.forge/handlers`).
2. Computes the `StructuredDiff` via the installed subprocess handler.
3. Starts a loopback-only HTTP server serving: a minimal static shell page,
   the renderer bundle from `~/.forge/renderers/`, the diff JSON, and blob
   endpoints backed by the git object database / working tree.
4. Opens the default browser; exits on Ctrl-C.

No blob ever leaves the machine; no network access is required. A later
`forge mergetool --web` reuses the same shell with `mode: "merge"`, writing
the resolution back through the existing mergetool path.

## 4. Compute tiers

| Tier | Diff computed by | Crosses the wire | Role |
|---|---|---|---|
| **S — Server** (default) | ForgeHub API handler, cached in `diffCache` | small `StructuredDiff` JSON | **Canonical.** The record for review/approval. Works on any client. |
| **B — Browser** (opt-in) | WASM handler build in the viewer's browser | both raw blobs + wasm + renderer | Offloads server CPU. Viewing convenience only. |
| **L — Local** (opt-in) | Local forge binary via `forge diff --web` | nothing (blobs already in the clone) | Cheapest for large assets; zero server involvement. |

Invariants:

- **Tier S remains canonical.** Client-computed diffs are a viewing
  convenience; anything review-shaped (approvals, merge decisions recorded by
  the server) refers to the server-computed diff.
- **Build pinning gives consistency.** Tiers B and L use the build pinned in
  the repo's `.forge/handlers`. If the executing build differs from the
  server's, the UI must say so rather than silently render a different diff.
- **Tier availability is capability-detected**, never assumed: Tier B
  requires a `wasm` asset in the manifest and acceptable blob sizes; Tier L
  requires a local clone.
- **Which tier is even *allowed* is trust-gated (§7):** the server (Tier S)
  runs *official* handlers only; *community* handlers are never computed
  server-side and must fall to a sandboxed client tier.

## 5. Mode selection UX (ForgeHub)

- **Quiet by default.** A small mode indicator on the diff/merge header
  ("rendered on server · switch"). No machinery until there is friction.
- **Reactive nudge.** If the Tier-S request exceeds a latency threshold (or
  the payload is known-large up front), replace the spinner with an offer:
  "taking longer than usual — render on your machine instead?"
- **Honest costs.** The Tier-B option displays the download it implies
  ("Render in browser — downloads 2 × 84 MB"); it is hidden entirely when
  capability detection fails. Tier L is offered as **"Open in forge"** — a
  copyable `forge diff --web <file> <base>..<head>` command (deep link
  protocol optional, later).
- **Sticky preference.** The chosen mode persists per format (and optionally
  per repo/device); a global "prefer client-side rendering" lives in user
  settings. Toggle usage is itself a signal of where Tier S is too slow.
- **Merge is phased separately.** Client-side merge *resolution* uploads a
  resolved blob for server validation — a bigger contract than read-only
  diff viewing. Not in the first iteration.

## 6. Security model

- Renderer bundles and WASM builds ride the **same trust chain as handler
  binaries** (already executed natively today): content-hash builds, hashes
  recorded in installed metadata, pinned per-repo via `.forge/handlers`.
- Bundles are self-contained; the forge local shell serves everything from
  loopback and sets a CSP that blocks all external requests, so a bundle
  cannot exfiltrate blob data.
- ForgeHub executing *official* bundles is equivalent to today's vendored
  viewers. Community bundles run only client-side, sandboxed and consented —
  the full model is §7, which also promotes SPEC.md open question 5 (the
  iframe/postMessage sandbox) from an open question to a concrete requirement.

## 7. Trust & execution model

The organizing principle: **who executes a handler is decided by who bears the
risk, and the marker of trust is a *signature*, not a *location*.** This is the
security backbone under the compute tiers (§4) — the tier a handler is allowed
to run in follows directly from whether it is trusted.

### 7a. The invariant

- **Official (signed) handler → the server runs it**, in-origin, no per-user
  consent. ForgeHub vouches for it.
- **Community (unsigned) handler → the client runs it**, in a sandboxed,
  consented browser context (§7e) or locally via forge. The server never
  fetches or executes it.
- The signature *is* the boundary: one bit — signed or not — decides
  server-vs-client execution. Everything else follows.

### 7b. "Official" is a property, not a place

- **Bootstrap (shipped, ForgeHubProject/ForgeHub#59):** official = an explicit
  format→handler map plus a pinned official release base URL. A *location*
  allowlist. Correct and shippable as v0.
- **Target:** official = the build carries a **signature that verifies against
  a key ForgeHub trusts**. Location-independent — a signed handler can live in
  any repo, release, or third-party registry and still be server-runnable. FHR
  becomes a *trust index* (signing keys + attested hashes), not necessarily the
  *host*; registry federation falls out for free.
- **What is signed: the content hash of the wasm build** — the exact bytes that
  execute, not just a manifest line. This reuses forge's content-hash-per-build
  model (forge#21/#23): a new build is a new hash is a new signature, i.e.
  re-vetting per build, which is the correct granularity. It also hardens the
  #59 bootstrap, which today trusts "whatever the release URL serves" — with a
  signature the server runs the wasm only if `sha256(bytes)` matches the
  attested, signed hash, defeating a compromised host or MITM swap.
- **Revocable:** the trust list (keys / attested hashes) is updatable; a handler
  that turns out malicious loses trust with no code change.

### 7c. Promotion lifecycle

```
community  ──published──▶  client-sandboxed + consented
    │
    └── vetted by maintainers ──▶ signed ──▶ official (server-run, no consent)
```

Same artifact throughout; the signature is the graduation certificate.
Demotion is just revoking the signature/hash from the trust list. This gives
the ecosystem a healthy path: anyone can ship a handler (client-only), and the
good ones graduate to first-class server execution.

### 7d. Server-side execution (official only)

- Verify **signature + content hash** before instantiating a handler's wasm.
  (#59 shipped the wasm runner and the location-map bootstrap; signature
  verification *replaces* the map.)
- Run sandboxed — wasm with a minimal fs stub, no host filesystem/network —
  and size-capped. Hardening targets: **worker-thread isolation + an execution
  timeout** (a synchronous wasm call cannot be interrupted from JS, so even a
  trusted handler on crafted input is a DoS risk).
- **Never** read a repo's `.forge/handlers` source URLs or any
  `~/.forge/sources.list`. The repo declares *which extensions* it wants
  handled (`.forge/formats`); the server maps those to *official* handlers
  only. A repo cannot redirect the server to a community source.

### 7e. Client-side execution (community — sandboxed + consented)

The concrete form of the iframe sandbox previously left open (SPEC.md open
question 5).

- **Isolation:** the community handler's wasm (compute) and renderer bundle
  (display) run inside an **iframe on a separate origin** (a sandboxed
  subdomain, or a `sandbox`-attributed frame with a null origin) — never
  ForgeHub's main origin. Untrusted code at the main origin could read the
  viewer's session token and act as them (XSS-class); a separate origin denies
  it ForgeHub's cookies and storage.
- **Protocol:** the host passes `{ mode, diff?, blobs }` in and receives
  rendered output / a `resolved` blob back over `postMessage`. Inside the
  frame, CSP is `connect-src 'none'` — the frame gets the repo's own blob bytes
  from the host and can reach nothing else (no auth token, no network).
- **Consent:** first use of a community handler for a repo prompts the viewer —
  *"This repo uses community handler X from source Y. Run it in your browser?"*
  — a sticky, revocable, per-source decision. This mirrors forge's local model,
  where the user themselves chose to `forge source add`.
- **No server involvement:** the server neither fetches nor proxies community
  handlers/renderers; the client fetches them from the community source and
  runs them sandboxed. The official `/renderers` proxy and the server wasm
  runner stay official-only, permanently.
- **Optional local path:** a viewer who already installed the handler via forge
  can "Open in forge" (Tier L) instead — zero in-browser execution. A
  power-user shortcut, not a requirement.

### 7f. Key management (the hard part, design before building)

Signature systems live or die on key handling: who holds the signing key, how
it reaches self-hosted instances, rotation, revocation. Prior art to borrow:
**Sigstore** (keyless, OIDC-identity signing + a transparency log) and **TUF**
(signed, role-separated, revocable trust roots). Design targets: a self-hosted
instance ships with the fhr-official trust root and may add its own trusted
signers; per-build signatures over wasm content hashes; a published revocation
list. This is the piece to design deliberately first — the #59 map bootstrap
lets ForgeHub deliver value while it is designed.

## 8. Phasing

1. **P1 — Contract + first artifacts (landed):** `mount()` contract types in
   `@fhr/types`; `@fhr/renderer-sdk`; a reference `renderer-gltf-scene` bundle
   shipping the lite DOM change-tree view; a `wasm` build of the handler
   (`syscall/js`, exposing `diff`/`merge`/`info`); manifest §2c keys; release
   workflow builds + attaches the wasm build and renderer bundle. **Deferred
   within P1:** the interactive three.js viewport (`view` mode) — ported from
   ForgeHub's `GltfSceneView` — drops in behind the same `mount()` contract
   later without a contract change.
2. **P2 — forge (landed):** `forge formats add` installs renderers;
   `forge diff --web`.
3. **P3 — ForgeHub adoption (landed):** web app loads FHR bundles instead of
   hardcoded viewers; server computes diffs with the official wasm handler
   (ForgeHubProject/ForgeHub#59, the location-map bootstrap of §7b).
4. **P4 — Signature trust (§7b, §7f):** sign per-build wasm hashes + a trust
   root/revocation list in FHR; ForgeHub verifies signature + hash before
   server execution, replacing the #59 location map. Hardening: worker-thread
   isolation + timeout for the server wasm.
5. **P5 — Community sandbox (§7e):** the consented, cross-origin iframe for
   client-side community handler compute + render; per-source consent UI.
6. **P6 — Tier B (official):** in-browser WASM compute for official handlers
   behind the §5 toggle (perf/cost, not trust).
7. **P7 — Merge:** `forge mergetool --web` and ForgeHub client-side merge
   resolution.

## 9. Open questions

1. ~~`mount()` wrapper generation: hand-written per package vs. a small
   `@fhr/renderer-sdk` build helper.~~ **Resolved:** shipped `@fhr/renderer-sdk`
   with `defineRenderer({ handlerId, extensions, render })`. The `render`
   callback is `(container, props) => cleanup?` — framework-agnostic, so a
   pure-DOM renderer and a React renderer (`createRoot(container).render(…)`
   returning `() => root.unmount()`) are both wrapped by the same SDK. The SDK
   also provides pure helpers (`flattenDiff`, `diffSummary`, `formatValue`) and
   a self-contained `renderDiffTree` DOM view.
2. Go `wasm` binary size: the standard-Go build of `handler-gltf-scene` is
   ~3.7 MB uncompressed (~1 MB gzipped over the wire). Acceptable for P1;
   revisit TinyGo only if Tier B adoption makes the download a real cost.
3. Blob-size ceiling for offering Tier B at all (proposal: hide above
   200 MB combined).
4. Deep-link protocol (`forge://…`) vs. copyable command for "Open in forge"
   (proposal: copyable command first; protocol handlers are per-OS pain).
5. ~~Does ForgeHub server ever adopt the WASM builds itself (ForgeHub#59
   Option B)?~~ **Resolved:** yes — shipped. ForgeHub runs the official wasm
   handler server-side (the location-map bootstrap of §7b); the diff now
   matches forge's output exactly.
6. **Signing scheme specifics (§7f):** Sigstore-style keyless vs. a static TUF
   trust root for self-hosted instances; what identity signs fhr-official
   builds. Design item for P4.
7. **Community sandbox origin (§7e):** dedicated sandbox subdomain vs. a
   `sandbox`-attributed null-origin frame — the former needs deploy/DNS
   support, the latter is self-contained but more restricted. Design item for
   P5.
