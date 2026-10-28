// Self-contained styles for the image compare — a renderer bundle cannot rely
// on host CSS. Scoped under .fhr-img, theme-aware via [data-theme], with the
// palette of the change tree (dom.ts) so the two read as one surface.

export const IMAGE_STYLE = `
.fhr-img { font: 13px/1.5 ui-sans-serif, system-ui, sans-serif; color: #1f2328;
  --fhr-ca: #ffffff; --fhr-cb: #eaeef2; --fhr-line: #d0d7de; --fhr-muted: #57606a; --fhr-accent: #0969da;
  --fhr-well: #f6f8fa; --fhr-float: rgba(255,255,255,0.92); }
.fhr-img[data-theme="dark"] { color: #e6edf3;
  --fhr-ca: #161b22; --fhr-cb: #21262d; --fhr-line: #30363d; --fhr-muted: #8b949e; --fhr-accent: #2f81f7;
  --fhr-well: #0d1117; --fhr-float: rgba(22,27,34,0.92); }
.fhr-img button { font: inherit; color: inherit; }
.fhr-img__bar { display: flex; flex-wrap: wrap; gap: 8px 16px; align-items: center; margin-bottom: 8px; }
.fhr-img__views { display: inline-flex; border: 1px solid var(--fhr-line); border-radius: 6px; overflow: hidden; }
.fhr-img__view { font-size: 12px; padding: 4px 10px; border: 0; background: transparent; cursor: pointer; }
.fhr-img__view + .fhr-img__view { border-left: 1px solid var(--fhr-line); }
.fhr-img__view[aria-selected="true"] { background: var(--fhr-accent); color: #fff; }
.fhr-img__toggle { display: inline-flex; gap: 6px; align-items: center; font-size: 12px; cursor: pointer; user-select: none; }
.fhr-img__toggle input { margin: 0; }
.fhr-img__note { display: flex; flex-wrap: wrap; gap: 4px 10px; align-items: center; font-size: 12px; color: var(--fhr-muted);
  margin-bottom: 8px; }
.fhr-img__note:empty { display: none; }
.fhr-img__link { padding: 0; border: 0; background: none; color: var(--fhr-accent); cursor: pointer; font-size: 12px; }
.fhr-img__link:hover { text-decoration: underline; }
.fhr-img__panes { display: flex; flex-wrap: wrap; gap: 12px; }
.fhr-img__pane { flex: 1 1 280px; min-width: 0; }
.fhr-img__label { display: flex; gap: 8px; font-size: 12px; color: var(--fhr-muted); margin-bottom: 4px; }
.fhr-img__label strong { color: inherit; font-weight: 600; }
.fhr-img__port { position: relative; overflow: hidden; border: 1px solid var(--fhr-line); border-radius: 6px;
  background: var(--fhr-well); user-select: none; -webkit-user-select: none; }
.fhr-img__port[data-pan="1"] { cursor: grab; touch-action: none; }
.fhr-img__port[data-dragging="1"] { cursor: grabbing; }
.fhr-img__stage { position: absolute; left: 0; top: 0; transform-origin: 0 0; }
.fhr-img__stage[data-pixelated="1"] { image-rendering: pixelated; }
.fhr-img__layer { position: absolute; left: 0; top: 0; display: block; max-width: none; max-height: none;
  background: repeating-conic-gradient(var(--fhr-ca) 0 25%, var(--fhr-cb) 0 50%) 0 0 / var(--fhr-check, 16px) var(--fhr-check, 16px); }
.fhr-img__layer[data-absent="1"] { visibility: hidden; }
.fhr-img__overlay { position: absolute; left: 0; top: 0; display: block; pointer-events: none; }
.fhr-img__bounds { position: absolute; pointer-events: none; outline: 2px dashed #e5007f; outline-offset: 2px; }
.fhr-img__zoom { position: absolute; right: 8px; bottom: 8px; display: flex; border: 1px solid var(--fhr-line);
  border-radius: 6px; background: var(--fhr-float); overflow: hidden; cursor: default; }
.fhr-img__zoom button { font-size: 12px; min-width: 28px; padding: 3px 7px; border: 0; background: transparent; cursor: pointer;
  font-variant-numeric: tabular-nums; }
.fhr-img__zoom button + button { border-left: 1px solid var(--fhr-line); }
.fhr-img__badge { position: absolute; top: 8px; font-size: 11px; padding: 1px 7px; border-radius: 4px;
  background: rgba(0,0,0,0.62); color: #fff; pointer-events: none; }
.fhr-img__divider { position: absolute; top: 0; bottom: 0; width: 16px; margin-left: -8px; cursor: ew-resize; touch-action: none; }
.fhr-img__divider::before { content: ""; position: absolute; top: 0; bottom: 0; left: 7px; width: 2px;
  background: #fff; box-shadow: 0 0 0 1px rgba(0,0,0,0.35); }
.fhr-img__divider::after { content: ""; position: absolute; top: 50%; left: 2px; width: 10px; height: 28px; margin-top: -14px;
  border-radius: 4px; background: #fff; box-shadow: 0 0 0 1px rgba(0,0,0,0.35); }
.fhr-img__divider:focus-visible::after { box-shadow: 0 0 0 2px var(--fhr-accent); }
.fhr-img__frames { margin-bottom: 8px; }
.fhr-img__frames:empty { display: none; }
.fhr-img__strip { display: flex; gap: 8px; align-items: center; min-width: 0; }
.fhr-img__play { font-size: 12px; min-width: 30px; padding: 3px 8px; border: 1px solid var(--fhr-line); border-radius: 6px;
  background: transparent; cursor: pointer; }
.fhr-img__cells { position: relative; display: flex; gap: 2px; overflow-x: auto; flex: 1 1 auto; min-width: 0; padding-bottom: 2px; }
.fhr-img__cell { flex: none; font: 11px/1.4 ui-monospace, monospace; min-width: 26px; padding: 2px 4px; border: 1px solid transparent;
  border-bottom: 3px solid transparent; border-radius: 4px; background: rgba(130,130,130,0.12); cursor: pointer; }
.fhr-img__cell--changed { border-bottom-color: #9a6700; }
.fhr-img__cell--added { border-bottom-color: #1a7f37; }
.fhr-img__cell--removed { border-bottom-color: #cf222e; }
.fhr-img[data-theme="dark"] .fhr-img__cell--changed { border-bottom-color: #d29922; }
.fhr-img[data-theme="dark"] .fhr-img__cell--added { border-bottom-color: #3fb950; }
.fhr-img[data-theme="dark"] .fhr-img__cell--removed { border-bottom-color: #f85149; }
.fhr-img__cell[aria-current="true"] { border-color: var(--fhr-accent); background: rgba(9,105,218,0.16); }
.fhr-img__frameinfo { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 2px 12px; margin-top: 4px;
  font-size: 12px; color: var(--fhr-muted); }
.fhr-img__marks { position: absolute; inset: 0; pointer-events: none; overflow: hidden; z-index: 1; }
.fhr-img__zoom, .fhr-img__divider, .fhr-img__badge { z-index: 2; }
.fhr-img__mark { position: absolute; box-sizing: border-box; border: 2px solid; border-radius: 3px; pointer-events: auto; cursor: pointer; }
.fhr-img__mark--modified { border-color: #d4a72c; }
.fhr-img__mark--added { border-color: #1a7f37; }
.fhr-img__mark--removed { border-color: #cf222e; }
.fhr-img__mark--layout { border-color: #8c959f; border-style: dashed; }
.fhr-img[data-theme="dark"] .fhr-img__mark--modified { border-color: #e3b341; }
.fhr-img[data-theme="dark"] .fhr-img__mark--added { border-color: #3fb950; }
.fhr-img[data-theme="dark"] .fhr-img__mark--removed { border-color: #f85149; }
.fhr-img__mark--hot { border-width: 3px; background: rgba(212,167,44,0.16); }
.fhr-img__mark--added.fhr-img__mark--hot { background: rgba(26,127,55,0.16); }
.fhr-img__mark--removed.fhr-img__mark--hot { background: rgba(207,34,46,0.16); }
.fhr-img__meta { margin-top: 12px; }
.fhr-img__meta:empty { display: none; }
`;
