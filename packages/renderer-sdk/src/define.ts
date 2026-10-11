import type { MountProps, RendererBundle, RendererInstance, RendererMode } from "@fhr/types";
import { renderSourceView } from "./source.js";

/** Optional teardown returned by a render pass. */
export type RenderCleanup = void | (() => void);

/**
 * Draw the renderer's UI into `container` for the given props. May return a
 * cleanup function that runs before the next render and on unmount.
 *
 * Pure-DOM authors build nodes directly. React authors:
 *   const root = createRoot(container);
 *   root.render(<MyRenderer {...props} />);
 *   return () => root.unmount();
 */
export type RenderFn = (container: HTMLElement, props: MountProps) => RenderCleanup;

/**
 * Optional non-destructive update. Without it, every host prop push is a full
 * teardown-and-redraw — which for a heavy renderer means destroying the WebGL
 * context, re-fetching and re-parsing the model blobs, and losing the camera,
 * even when all the host changed was which row is selected.
 *
 * Implement it to patch the existing DOM in place instead. Contract:
 *
 *   * `prev` is the last props actually applied, so a renderer can diff the two
 *     and decide what it can patch.
 *   * Return `false` to *decline* this particular push — the caller then runs
 *     the default teardown path. Declining is the honest answer whenever the
 *     change reaches something the renderer built at mount time (new blobs, a
 *     new diff, a different mode). Any other return value (including
 *     `undefined`) means "handled".
 *   * The cleanup from the original `render` pass stays in force; an update is
 *     not a new render pass and does not replace it.
 *
 * Renderers that don't implement it keep exactly today's behaviour.
 */
export type UpdateFn = (
  container: HTMLElement,
  props: MountProps,
  prev: MountProps,
) => boolean | void;

export type DefineRendererOptions = {
  handlerId: string;
  extensions: string[];
  /** Content-hash build stamped at bundle-build time (matches binary + wasm). */
  build?: string;
  render: RenderFn;
  /** See UpdateFn. Omit for the default teardown-and-redraw on every push. */
  update?: UpdateFn;
  /**
   * The modes `render` draws. Omit when it draws all three. A renderer that only
   * draws diffs lists `["diff"]`: hosts then show a single file with their own
   * viewer, and a host that mounts an unlisted `view` anyway gets the file's
   * text instead of `render`. An unlisted `diff` or `merge` still reaches
   * `render`, which always has a diff to draw.
   */
  modes?: RendererMode[];
};

/**
 * Turn a single render function into a RendererBundle with mount/update/unmount
 * lifecycle handled for you. This is the framework-agnostic bundle boundary —
 * consumers call the returned bundle's mount() without knowing what's inside.
 */
export function defineRenderer(opts: DefineRendererOptions): RendererBundle {
  const modes = opts.modes ? [...opts.modes] : undefined;
  // A `view` the renderer does not draw gets the file's text (see `modes`).
  const floored = (p: MountProps): boolean => p.mode === "view" && modes !== undefined && !modes.includes("view");
  return {
    fhrVersion: 1,
    handlerId: opts.handlerId,
    extensions: opts.extensions,
    build: opts.build,
    ...(modes ? { modes } : {}),
    mount(el: HTMLElement, props: MountProps): RendererInstance {
      let cleanup: RenderCleanup;
      // The props the visible DOM was last built or patched for — what an
      // update hook needs to diff against, and what it must be updated to
      // whether the push was patched in place or fully redrawn.
      let applied: MountProps = props;
      const draw = (p: MountProps) => {
        if (typeof cleanup === "function") cleanup();
        el.replaceChildren();
        cleanup = floored(p) ? renderSourceView(el, p) : opts.render(el, p);
        applied = p;
      };
      draw(props);
      return {
        update: (p: MountProps) => {
          // The hook only ever patches what `render` built.
          if (opts.update && !floored(p) && !floored(applied) && opts.update(el, p, applied) !== false) {
            applied = p;
            return;
          }
          draw(p);
        },
        unmount: () => {
          if (typeof cleanup === "function") cleanup();
          el.replaceChildren();
        },
      };
    },
  };
}
